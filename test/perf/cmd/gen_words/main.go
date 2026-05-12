package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"time"
)

var (
	endpoint   = flag.String("endpoint", "http://127.0.0.1:18080/api/v1/words/import", "import endpoint URL")
	apiKey     = flag.String("api-key", "censorhub-default-key", "X-API-Key header value")
	totalN     = flag.Int("n", 10000, "total words to generate")
	batchSz    = flag.Int("batch", 500, "batch size per import request")
	outputFile = flag.String("out", "test/perf/data/words.txt", "path to write generated word list")
)

// 常用汉字池与拉丁字母池构造词条，长度 2-6
var cjkPool = []rune("政府经济社会文化科技体育娱乐国际新闻财经股票基金证券银行保险房产旅游教育培训医疗健康汽车科学研究学术论文专业技能编程开发设计艺术音乐影视明星网游电竞直播短视频购物外卖快递物流安全隐私欺诈谣言违规涉政赌博毒品暴力血腥色情低俗歧视仇恨恐吓骚扰垃圾广告")
var asciiPool = []rune("abcdefghijklmnopqrstuvwxyz")

type createWord struct {
	Text     string `json:"text"`
	Category string `json:"category"`
	Level    int    `json:"level"`
	Tag      string `json:"tag,omitempty"`
}

type importReq struct {
	Words []createWord `json:"words"`
}

func randWord(rng *rand.Rand) string {
	// 70% 中文, 30% 英文
	if rng.Float64() < 0.7 {
		n := 2 + rng.Intn(4) // 2..5
		buf := make([]rune, n)
		for i := range buf {
			buf[i] = cjkPool[rng.Intn(len(cjkPool))]
		}
		return string(buf)
	}
	n := 3 + rng.Intn(6) // 3..8
	buf := make([]rune, n)
	for i := range buf {
		buf[i] = asciiPool[rng.Intn(len(asciiPool))]
	}
	return string(buf)
}

func main() {
	flag.Parse()
	total := *totalN
	rng := rand.New(rand.NewSource(42))
	seen := map[string]struct{}{}
	words := make([]createWord, 0, total)
	cats := []string{"politics", "porn", "abuse", "ad", "violence", "custom"}
	for len(words) < total {
		w := randWord(rng)
		if _, ok := seen[w]; ok {
			continue
		}
		seen[w] = struct{}{}
		words = append(words, createWord{
			Text:     w,
			Category: cats[rng.Intn(len(cats))],
			Level:    1 + rng.Intn(4),
		})
	}

	cli := &http.Client{Timeout: 60 * time.Second}
	imported, skipped, failed := 0, 0, 0
	start := time.Now()
	for i := 0; i < total; i += *batchSz {
		end := i + *batchSz
		if end > total {
			end = total
		}
		body, _ := json.Marshal(importReq{Words: words[i:end]})
		req, _ := http.NewRequest("POST", *endpoint, bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-API-Key", *apiKey)
		resp, err := cli.Do(req)
		if err != nil {
			fmt.Fprintln(os.Stderr, "request error:", err)
			os.Exit(1)
		}
		rbody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode >= 400 {
			fmt.Fprintf(os.Stderr, "HTTP %d: %s\n", resp.StatusCode, string(rbody))
			os.Exit(1)
		}
		var r struct {
			Code int `json:"code"`
			Data struct {
				Total    int `json:"total"`
				Imported int `json:"imported"`
				Skipped  int `json:"skipped"`
				Failures []struct {
					Reason string `json:"reason"`
				} `json:"failures"`
			} `json:"data"`
		}
		if err := json.Unmarshal(rbody, &r); err != nil {
			fmt.Fprintln(os.Stderr, "decode error:", err, "body:", string(rbody))
			os.Exit(1)
		}
		imported += r.Data.Imported
		skipped += r.Data.Skipped
		failed += len(r.Data.Failures)
		fmt.Printf("[%5d/%5d] imported=%d skipped=%d failed=%d\n", end, total, r.Data.Imported, r.Data.Skipped, len(r.Data.Failures))
	}

	// 把词表写到文件，供 wrk 请求体随机选择用
	if err := os.MkdirAll(filepath.Dir(*outputFile), 0o755); err != nil {
		fmt.Fprintln(os.Stderr, "mkdir error:", err)
		os.Exit(1)
	}
	f, err := os.Create(*outputFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create file error:", err)
		os.Exit(1)
	}
	defer f.Close()
	for _, w := range words {
		fmt.Fprintln(f, w.Text)
	}

	fmt.Printf("\nDone in %s. imported=%d skipped=%d failed=%d\n",
		time.Since(start).Round(time.Millisecond), imported, skipped, failed)
}
