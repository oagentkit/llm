// langfuse_read.go — Langfuse Public API 只读客户端（v3 self-hosted 契约面，
// BasicAuth pk:sk）。列表端点不含 observations，FetchBatch 的逐条详情合并是
// 必要成本；顺序串行，对自托管服务友好。
//
// # 为什么与写端同域
//
// 本包此前只有**写/发射端**（otelgen.go 的 OTel generation span 出口）。
// 读端（拉回 trace 做观测报告）与写端分居两仓时，「Langfuse 上有什么、
// 是谁写进去的」这件事没有一处能答全 ⇒ 读写同域，一包两看。
//
// # 错误口径
//
// 走 oekit/pkg/errors（与 stdlib 同型透传，Is/As 判定单 import 即可）。
// 包文档归 tracing.go。

package obsx

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/oekit/concurrency/fanout"
	"github.com/oekit/pkg/errors"
	"github.com/oekit/pkg/httpjson"
)

// detailFetchConcurrency 详情拉取并发上界：单条 GET 是轻请求，上界只防
// limit 数百时对 langfuse API 的瞬时压力（翻页仍串行——页序推进带提前终止）。
const detailFetchConcurrency = 4

// Client Langfuse 只读客户端。
type Client struct {
	host string
	pk   string
	sk   string
	http *http.Client
	// HTTP 可选注入的 *http.Client（nil = 30s 缺省——httpjson「调用方注入
	// Client」分工的兑现面，此前全仓唯一不可注入的 HTTP 消费方）
	HTTP *http.Client
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return c.http
}

// NewClient host 形如 https://lf.example.com（不带尾斜杠）。
func NewClient(host, publicKey, secretKey string) *Client {
	return &Client{
		host: strings.TrimRight(host, "/"),
		pk:   publicKey,
		sk:   secretKey,
		http: &http.Client{Timeout: 30 * time.Second},
	}
}

// Query 选择口径（selection criteria——观测报告必须可复述选择过程）。
type Query struct {
	Limit    int    // 最多拉取的 trace 数（<=0 → 单页默认 50）
	PageSize int    // 每页条数（<=0 → 50）
	Name     string // trace name 过滤
	Session  string // sessionId 过滤
	User     string // userId 过滤
	From     string // fromTimestamp（RFC3339/ISO8601）
	To       string // toTimestamp
	Tags     []string
}

// Batch 一次拉取结果；TotalItems 是服务端过滤后的总选择集大小（含未拉取部分）。
type Batch struct {
	Traces     []Trace
	TotalItems int

	// Skipped 详情拉取失败被跳过的条数（两段式降级计数——单条 404/瞬时失败
	// 不再弃掉整批；调用方可据此决策重试）
	Skipped int
}

type listResp struct {
	Data []Trace `json:"data"`
	Meta struct {
		Page       int `json:"page"`
		Limit      int `json:"limit"`
		TotalItems int `json:"totalItems"`
		TotalPages int `json:"totalPages"`
	} `json:"meta"`
}

// FetchBatch 按选择口径拉取 trace（列表分页 + 逐条详情合并 observations）。
func (c *Client) FetchBatch(ctx context.Context, q Query) (Batch, error) {
	pageSize := q.PageSize
	if pageSize <= 0 {
		pageSize = 50
	}
	limit := q.Limit
	if limit <= 0 {
		limit = pageSize
	}

	// 两段式：先翻页收齐轻量列表（可整段重试，失败=致命），
	// 再逐条详情（单条失败降级跳过计数——列表取回后 trace 被删的 404 不再
	// 弃掉整批已合并详情；**但整体 ctx 取消/超时例外**，与列表阶段同口径
	// 上抛）。seen 去重：删除/补录旧时间戳 trace 会使后续页
	// 整体位移，跨页 ID 去重兜底。
	type row struct {
		id   string
		name string
	}
	var rows []row
	seen := map[string]bool{}
	var total int
	for page := 1; len(rows) < limit; page++ {
		u := fmt.Sprintf("%s/api/public/traces?%s", c.host, listQuery(q, page, pageSize).Encode())
		var resp listResp
		if err := c.get(ctx, u, &resp); err != nil {
			return Batch{}, err // 翻页失败=致命（此时无部分产物可保）
		}
		total = resp.Meta.TotalItems
		for _, tr := range resp.Data {
			if len(rows) >= limit {
				break
			}
			if tr.ID == "" || seen[tr.ID] {
				continue
			}
			seen[tr.ID] = true
			rows = append(rows, row{id: tr.ID, name: tr.Name})
		}
		if page >= resp.Meta.TotalPages || len(resp.Data) == 0 {
			break
		}
	}
	var batch Batch
	batch.TotalItems = total
	// 详情拉取收编 fanout.Map（有界并行——N+1 串行是 FetchBatch 的墙钟大头）：
	// 结果保持 rows 序=timestamp.asc 列表序；逐条降级语义不变，失败/panic 均
	// 计 Skipped 不弃整批。
	details, derr := fanout.Map(ctx, rows, detailFetchConcurrency, func(ctx context.Context, r row) (Trace, error) {
		return c.GetTrace(ctx, r.id)
	})
	failedIdx := map[int]bool{}
	if derr != nil {
		// 整体取消/超时先判别：ctx 取消不是「单条失败降级」那一档
		// ——降级档的适用范围写得很窄（trace 被删的 404 这类单条失败），把
		// 整体取消并进去会返回 err==nil 的空批次，调用方无从分辨「trace 被删
		// 了」与「我被取消了」。与列表阶段同口径上抛。fanout.Error.Unwrap 支持
		// 多错误穿透（Go 1.20+），errors.Is 能从任意失败项里判出 ctx 错误。
		//
		// 但 errors.Is 单独用不够：net/http client 自身 Timeout
		// 的 timeoutError.Is 认 context.DeadlineExceeded——30s client 超时与
		// 「整体 ctx 超时」在 errors.Is 下不可分，单条慢 trace 会把整批判成
		// 整体超时上抛（本应 49 成功 + 1 Skipped）。收口：还须调用方 ctx
		// **真的已死**（ctx.Err() != nil）——client 超时发生时调用方 ctx 往往
		// 还活着，这正是可用的判别信号。ctx 活着 ⇒ 非整体超时 ⇒ 单条降级档。
		if ctx.Err() != nil && (errors.Is(derr, context.Canceled) || errors.Is(derr, context.DeadlineExceeded)) {
			return batch, derr
		}
		var fe *fanout.Error
		if errors.As(derr, &fe) {
			batch.Skipped = len(fe.Failed) // 单条详情失败：降级跳过计数，不弃整批
			for _, f := range fe.Failed {
				failedIdx[f.Index] = true
			}
		}
	}
	for i, d := range details {
		if failedIdx[i] {
			continue
		}
		if d.Name == "" {
			d.Name = rows[i].name
		}
		batch.Traces = append(batch.Traces, d)
	}
	return batch, nil
}

// GetTrace 单条 trace 详情（含 observations）。
func (c *Client) GetTrace(ctx context.Context, id string) (Trace, error) {
	var tr Trace
	u := fmt.Sprintf("%s/api/public/traces/%s", c.host, url.PathEscape(id))
	if err := c.get(ctx, u, &tr); err != nil {
		return Trace{}, err
	}
	return tr, nil
}

func listQuery(q Query, page, pageSize int) url.Values {
	v := url.Values{}
	v.Set("page", strconv.Itoa(page))
	v.Set("limit", strconv.Itoa(pageSize))
	v.Set("orderBy", "timestamp.asc")
	if q.Name != "" {
		v.Set("name", q.Name)
	}
	if q.Session != "" {
		v.Set("sessionId", q.Session)
	}
	if q.User != "" {
		v.Set("userId", q.User)
	}
	if q.From != "" {
		v.Set("fromTimestamp", q.From)
	}
	if q.To != "" {
		v.Set("toTimestamp", q.To)
	}
	for _, t := range q.Tags {
		v.Add("tags", t)
	}
	return v
}

func (c *Client) get(ctx context.Context, u string, out any) error {
	// GET 幂等：短重试消化瞬时 5xx/429/网络超时——单次抖动不再炸整批
	//（FetchBatch 是 N+1 串行，此前任一瞬时失败丢弃全部已合并详情）。
	err := httpjson.DoJSONWithRetry(ctx, c.httpClient(),
		func() (httpjson.Request, error) {
			return httpjson.Request{
				URL: u,
				Header: func(h http.Header) {
					h.Set("Authorization", "Basic "+basicAuth(c.pk, c.sk))
				},
			}, nil
		},
		httpjson.RetryConfig{MaxAttempts: 3, BaseDelay: 200 * time.Millisecond, MaxDelay: 2 * time.Second},
		out)
	if err != nil {
		return errors.Wrap(err, "langfuse")
	}
	return nil
}

// basicAuth RFC 7617 Basic 凭证（pk:sk，URL-safe 编码不受内容影响）。
func basicAuth(user, pass string) string {
	return base64.StdEncoding.EncodeToString([]byte(user + ":" + pass))
}
