package obsx

// 本文件钉「HTTP client 自身 Timeout 的错误在 errors.Is 下与「整体 ctx
// DeadlineExceeded」不可分（net/http 的 timeoutError.Is 认
// context.DeadlineExceeded），单条慢 trace 曾把整批 FetchBatch 上抛而非
// 降级跳过」的回归（契约面随实现自 oagentkit/obs 迁入本仓，回归面原样保留）。

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/oekit/pkg/errors"
)

// TestFetchBatchClientTimeoutDegradesNotPropagates 锚点：单条 GetTrace 超过
// client Timeout（调用方 ctx 仍有效）时整批按 Skipped 降级、不上抛。
// 判别信号 = ctx.Err() 仍为 nil；修复前 errors.Is 单判把
// client 超时归入「整体超时」档，err 非 nil 且 0 条产物。
func TestFetchBatchClientTimeoutDegradesNotPropagates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/public/traces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"page": 1, "limit": 50, "totalItems": 2, "totalPages": 1},
			"data": []map[string]any{
				{"id": "tr-1", "timestamp": "2026-09-14T08:00:00Z", "name": "agent"},
				{"id": "tr-2", "timestamp": "2026-09-14T08:00:00Z", "name": "agent"},
			},
		})
	})
	mux.HandleFunc("/api/public/traces/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/api/public/traces/")
		if id == "tr-2" {
			// 慢详情：超过 client Timeout。客户端超时断开后 r.Context() 取消
			// 即返回，防 httptest.Server.Close 等 handler 挂住。
			select {
			case <-time.After(5 * time.Second):
			case <-r.Context().Done():
				return
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": id, "timestamp": "2026-09-14T08:00:00Z", "name": "agent",
		})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewClient(srv.URL, "pk", "sk")
	// 缺省 30s 太长测试跑不动；500ms 对本地列表请求足够宽（列表必须先成功，
	// 否则测到的是列表阶段而非 detail 判别），对 tr-2 的慢详情足够窄。
	c.HTTP = &http.Client{Timeout: 500 * time.Millisecond}

	batch, err := c.FetchBatch(context.Background(), Query{Limit: 2})
	if err != nil {
		t.Fatalf("client 超时（调用方 ctx 仍活）必须按单条失败降级，不得上抛整批: %v", err)
	}
	if batch.Skipped != 1 {
		t.Fatalf("慢 trace 应计 Skipped=1，实得 %+v", batch)
	}
	if len(batch.Traces) != 1 || batch.Traces[0].ID != "tr-1" {
		t.Fatalf("成功条必须保留（49+1 形态的 1+1 版）: %+v", batch.Traces)
	}
}

// TestFetchBatchCallerDeadlineStillPropagates 对照：调用方 ctx 自己到点
// （ctx.Err()!=nil）仍走整体超时上抛档——该收口不得把整体取消判别收掉。
func TestFetchBatchCallerDeadlineStillPropagates(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/public/traces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"meta": map[string]any{"page": 1, "limit": 50, "totalItems": 1, "totalPages": 1},
			"data": []map[string]any{{"id": "tr-1", "timestamp": "2026-09-14T08:00:00Z", "name": "agent"}},
		})
	})
	mux.HandleFunc("/api/public/traces/", func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-time.After(5 * time.Second):
		case <-r.Context().Done():
			return
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()

	batch, err := NewClient(srv.URL, "pk", "sk").FetchBatch(ctx, Query{Limit: 1})
	if err == nil {
		t.Fatal("调用方 ctx 到点必须整体上抛")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("上抛错误应可 errors.Is 命中 context.DeadlineExceeded，实得: %v", err)
	}
	if len(batch.Traces) != 0 {
		t.Fatalf("整体超时路径不该有完整 traces: %+v", batch)
	}
}
