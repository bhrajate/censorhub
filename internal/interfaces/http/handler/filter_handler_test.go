package handler

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"github.com/bhrajate/censorhub/internal/application/service"
	"github.com/bhrajate/censorhub/internal/domain/entity"
	"github.com/bhrajate/censorhub/internal/domain/valueobject"
	"github.com/bhrajate/censorhub/internal/infrastructure/algorithm"
)

func setupFilterHandler() (*FilterHandler, *gin.Engine) {
	gin.SetMode(gin.TestMode)

	engine := algorithm.NewACFilterEngine()
	engine.Rebuild([]*entity.SensitiveWord{
		{ID: 1, Text: "赌博", Category: valueobject.CategoryViolence, Level: valueobject.RiskHigh, Status: valueobject.WordStatusActive},
		{ID: 2, Text: "色情", Category: valueobject.CategoryPorn, Level: valueobject.RiskCritical, Status: valueobject.WordStatusActive},
		{ID: 3, Text: "fuck", Category: valueobject.CategoryAbuse, Level: valueobject.RiskMedium, Status: valueobject.WordStatusActive},
	})

	strategies := map[valueobject.FilterStrategyType]valueobject.FilterStrategy{
		valueobject.StrategyDetect:    algorithm.NewDetectStrategy(),
		valueobject.StrategyReplace:   algorithm.NewReplaceStrategy(),
		valueobject.StrategyHighlight: algorithm.NewHighlightStrategy(),
	}

	filterService := service.NewFilterAppService(engine, strategies, zap.NewNop())
	h := NewFilterHandler(filterService)

	r := gin.New()
	r.POST("/api/v1/filter/detect", h.Detect)
	r.POST("/api/v1/filter/replace", h.Replace)
	r.POST("/api/v1/filter/highlight", h.Highlight)
	r.POST("/api/v1/filter/batch", h.BatchDetect)

	return h, r
}

func TestFilterHandler_Detect_Hit(t *testing.T) {
	_, r := setupFilterHandler()

	body, _ := json.Marshal(map[string]string{"text": "这是赌博内容"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/filter/detect", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	data := resp["data"].(map[string]interface{})
	if !data["is_hit"].(bool) {
		t.Error("expected is_hit to be true")
	}
	if int(data["hit_count"].(float64)) != 1 {
		t.Errorf("expected hit_count 1, got %v", data["hit_count"])
	}
}

func TestFilterHandler_Detect_NoHit(t *testing.T) {
	_, r := setupFilterHandler()

	body, _ := json.Marshal(map[string]string{"text": "正常的文本内容"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/filter/detect", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	data := resp["data"].(map[string]interface{})
	if data["is_hit"].(bool) {
		t.Error("expected is_hit to be false")
	}
}

func TestFilterHandler_Replace(t *testing.T) {
	_, r := setupFilterHandler()

	body, _ := json.Marshal(map[string]string{"text": "这是赌博内容"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/filter/replace", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	data := resp["data"].(map[string]interface{})
	filtered := data["filtered"].(string)
	if filtered == "这是赌博内容" {
		t.Error("expected filtered text to have replacements")
	}
}

func TestFilterHandler_Highlight(t *testing.T) {
	_, r := setupFilterHandler()

	body, _ := json.Marshal(map[string]string{"text": "这是赌博内容"})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/filter/highlight", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	data := resp["data"].(map[string]interface{})
	filtered := data["filtered"].(string)
	if filtered == "这是赌博内容" {
		t.Error("expected filtered text to have highlight marks")
	}
}

func TestFilterHandler_BatchDetect(t *testing.T) {
	_, r := setupFilterHandler()

	body, _ := json.Marshal(map[string]interface{}{
		"texts": []string{"赌博网站", "正常文本", "fuck you"},
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/filter/batch", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Code)
	}

	var resp map[string]interface{}
	json.Unmarshal(w.Body.Bytes(), &resp)

	data := resp["data"].(map[string]interface{})
	if int(data["total"].(float64)) != 3 {
		t.Errorf("expected total 3, got %v", data["total"])
	}
	if int(data["hit_num"].(float64)) != 2 {
		t.Errorf("expected hit_num 2, got %v", data["hit_num"])
	}
}

func TestFilterHandler_InvalidRequest(t *testing.T) {
	_, r := setupFilterHandler()

	// 空 body
	req := httptest.NewRequest(http.MethodPost, "/api/v1/filter/detect", bytes.NewBuffer([]byte("{}")))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	r.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Code)
	}
}
