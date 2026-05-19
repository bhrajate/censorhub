// freshness 测量"写入一条新词到 detect 接口能命中"的端到端延迟分布。
//
// 流程(单轮):
//  1. 生成一个唯一的 ASCII 词条 perf_fresh_<round>_<rand>
//  2. POST /api/v1/words 写入,记录 t0
//  3. 立刻每 PollMillis 毫秒发起一次 detect,直到结果中包含该词
//  4. 记录 detection 命中时刻 t1,latency = t1 - t0
//
// 重复 N 轮,输出 p50/p90/p95/p99/max 与命中超时率。
//
// 用法:
//   go run ./test/perf/cmd/freshness \
//     -base http://127.0.0.1:18080 \
//     -api-key censorhub-default-key \
//     -rounds 100 \
//     -poll-ms 25 \
//     -timeout 5s
//
// 改动前(PubSub + 防抖)的预期: p50 ~500ms (debounce 周期), p99 可能 ≥3s (max-wait)
// 改动后(指纹轮询)的预期:     p50 ~250-500ms, p99 ≤ pollInterval+jitter ≈ 750ms
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"time"
)

var (
	baseURL  = flag.String("base", "http://127.0.0.1:18080", "server base URL")
	apiKey   = flag.String("api-key", "censorhub-default-key", "X-API-Key")
	rounds   = flag.Int("rounds", 100, "how many freshness probes to run")
	pollMs   = flag.Int("poll-ms", 25, "detect poll interval in ms")
	timeout  = flag.Duration("timeout", 5*time.Second, "per-round detection timeout")
	gap      = flag.Duration("gap", 100*time.Millisecond, "delay between rounds (let server poll re-stabilize)")
	parallel = flag.Int("parallel", 1, "concurrent rounds (1=sequential)")
	cleanup  = flag.Bool("cleanup", true, "delete probe words after measurement")
)

type createReq struct {
	Text     string `json:"text"`
	Category string `json:"category"`
	Level    int    `json:"level"`
	Tag      string `json:"tag,omitempty"`
}

type detectReq struct {
	Text string `json:"text"`
}

type detectResp struct {
	Code int `json:"code"`
	Data struct {
		IsHit   bool `json:"is_hit"`
		Matches []struct {
			Word string `json:"word"`
		} `json:"matches"`
	} `json:"data"`
}

type roundResult struct {
	id       int
	word     string
	wordID   uint64
	latency  time.Duration
	detected bool
	err      error
}

func main() {
	flag.Parse()
	if *rounds < 1 {
		fmt.Fprintln(os.Stderr, "rounds must be >= 1")
		os.Exit(2)
	}
	cli := &http.Client{Timeout: 10 * time.Second}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	suffix := rng.Int63()
	fmt.Printf("freshness probe: rounds=%d poll=%dms timeout=%v parallel=%d\n",
		*rounds, *pollMs, *timeout, *parallel)
	fmt.Printf("server: %s\n\n", *baseURL)

	results := make([]roundResult, *rounds)
	sem := make(chan struct{}, *parallel)
	var wg sync.WaitGroup
	for i := 0; i < *rounds; i++ {
		i := i
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			results[i] = runOne(cli, i, suffix)
			if *gap > 0 {
				time.Sleep(*gap)
			}
		}()
		// 简单进度提示
		if (i+1)%10 == 0 {
			fmt.Printf("scheduled %d/%d\n", i+1, *rounds)
		}
	}
	wg.Wait()

	// 输出超时/失败轮的明细,便于和 server log 对齐
	fmt.Println("\n========== TIMEOUT / ERROR DETAILS ==========")
	for _, r := range results {
		if r.err != nil {
			fmt.Printf("round=%d word_id=%d ERROR: %v\n", r.id, r.wordID, r.err)
		} else if !r.detected {
			fmt.Printf("round=%d word_id=%d word=%s TIMEOUT (waited %v)\n",
				r.id, r.wordID, r.word, r.latency)
		}
	}

	// 清理写入的词条,避免污染
	if *cleanup {
		fmt.Println("\nCleaning up probe words...")
		cleaned := 0
		for _, r := range results {
			if r.wordID != 0 {
				if deleteWord(cli, r.wordID) == nil {
					cleaned++
				}
			}
		}
		fmt.Printf("Deleted %d/%d probe words\n", cleaned, *rounds)
	}

	report(results)
}

func runOne(cli *http.Client, idx int, suffix int64) roundResult {
	r := roundResult{id: idx}
	r.word = fmt.Sprintf("perf_fresh_%d_%d", suffix, idx)

	t0 := time.Now()
	id, err := createWord(cli, r.word)
	if err != nil {
		r.err = fmt.Errorf("create: %w", err)
		return r
	}
	r.wordID = id

	// 立刻轮询 detect 直到命中或超时
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	tick := time.NewTicker(time.Duration(*pollMs) * time.Millisecond)
	defer tick.Stop()
	for {
		hit, err := detectHit(cli, r.word)
		if err == nil && hit {
			r.detected = true
			r.latency = time.Since(t0)
			return r
		}
		select {
		case <-ctx.Done():
			r.detected = false
			r.latency = *timeout
			return r
		case <-tick.C:
		}
	}
}

func createWord(cli *http.Client, text string) (uint64, error) {
	body, _ := json.Marshal(createReq{Text: text, Category: "custom", Level: 1, Tag: "perf-fresh"})
	req, _ := http.NewRequest("POST", *baseURL+"/api/v1/words", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", *apiKey)
	resp, err := cli.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return 0, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rb))
	}
	var r struct {
		Code int `json:"code"`
		Data struct {
			ID uint64 `json:"id"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rb, &r)
	return r.Data.ID, nil
}

func detectHit(cli *http.Client, target string) (bool, error) {
	body, _ := json.Marshal(detectReq{Text: target})
	req, _ := http.NewRequest("POST", *baseURL+"/api/v1/filter/detect", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", *apiKey)
	resp, err := cli.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return false, fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(rb))
	}
	var r detectResp
	if err := json.Unmarshal(rb, &r); err != nil {
		return false, err
	}
	if !r.Data.IsHit {
		return false, nil
	}
	for _, m := range r.Data.Matches {
		if m.Word == target {
			return true, nil
		}
	}
	return false, nil
}

func deleteWord(cli *http.Client, id uint64) error {
	url := fmt.Sprintf("%s/api/v1/words/%d", *baseURL, id)
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("X-API-Key", *apiKey)
	resp, err := cli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	return nil
}

func report(results []roundResult) {
	var lats []time.Duration
	timeouts := 0
	errs := 0
	for _, r := range results {
		if r.err != nil {
			errs++
			continue
		}
		if !r.detected {
			timeouts++
			continue
		}
		lats = append(lats, r.latency)
	}
	slices.Sort(lats)

	fmt.Println("\n========== FRESHNESS RESULTS ==========")
	fmt.Printf("Total rounds:   %d\n", len(results))
	fmt.Printf("Successful:     %d\n", len(lats))
	fmt.Printf("Timeouts:       %d\n", timeouts)
	fmt.Printf("Errors:         %d\n", errs)
	if len(lats) == 0 {
		fmt.Println("\nNo successful detections, cannot compute percentiles.")
		return
	}
	fmt.Println(strings.Repeat("-", 40))
	fmt.Printf("min:  %v\n", lats[0])
	fmt.Printf("p50:  %v\n", lats[len(lats)*50/100])
	fmt.Printf("p75:  %v\n", lats[len(lats)*75/100])
	fmt.Printf("p90:  %v\n", lats[len(lats)*90/100])
	fmt.Printf("p95:  %v\n", lats[min(len(lats)*95/100, len(lats)-1)])
	fmt.Printf("p99:  %v\n", lats[min(len(lats)*99/100, len(lats)-1)])
	fmt.Printf("max:  %v\n", lats[len(lats)-1])
	var sum time.Duration
	for _, d := range lats {
		sum += d
	}
	fmt.Printf("mean: %v\n", sum/time.Duration(len(lats)))
}
