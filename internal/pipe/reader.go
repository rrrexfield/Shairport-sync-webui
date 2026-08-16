package pipe

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"syscall"
	"time"
)

// maxLine 单行上限（含封面 base64 行）。超长行丢弃，防内存膨胀。
const maxLine = 1 << 20 // 1MB

// State 是 reader 的当前状态，供 /api/status 展示。
type State struct {
	Open       bool   `json:"open"`
	LastItemAt int64  `json:"last_item_at"`
	Error      string `json:"error"`
}

// Reader 常驻读取 metadata pipe。
// 读端以非阻塞方式打开并保持：WebUI 读端始终存在，shairport-sync
// 写端不会因无读者而阻塞；WebUI 重启期间写端由 pipe_timeout 兜底。
type Reader struct {
	mu         sync.Mutex
	path       string
	cache      *Cache
	conn       *os.File
	openAt     time.Time
	lastItemAt time.Time
	lastErr    string
}

// New 创建 Reader（不启动读循环）。
func New(path string) *Reader {
	return &Reader{path: path, cache: NewCache()}
}

// SetPath 热切换 pipe 路径（配置修改后调用），下一轮重开时生效。
func (r *Reader) SetPath(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.path = path
}

// Cache 暴露底层缓存（Snapshot 用）。
func (r *Reader) Cache() *Cache { return r.cache }

// Snapshot 返回字段级元数据快照。
func (r *Reader) Snapshot() Snapshot { return r.cache.Snapshot() }

// State 返回当前状态。
func (r *Reader) State() State {
	r.mu.Lock()
	defer r.mu.Unlock()
	st := State{
		Open:  r.conn != nil,
		Error: r.lastErr,
	}
	if !r.lastItemAt.IsZero() {
		st.LastItemAt = r.lastItemAt.Unix()
	}
	return st
}

// Run 常驻读循环，直到 ctx 取消。
// 重连策略：文件不存在 5s 重试；写端关闭（EOF）2s 重开；无数据（EAGAIN）100ms。
func (r *Reader) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			r.closeConn()
			return
		default:
		}
		kind, err := r.readOnce(ctx)
		if err == nil {
			r.clearErr()
		}
		// EOF 类退出立即重开（读端关闭窗口缩小到 open 系统调用级，
		// 阻塞 open 的写端几乎总能接入）；文件不存在 5s 重试。
		delay := time.Duration(0)
		if kind == reopenMissing {
			delay = 5 * time.Second
		}
		select {
		case <-ctx.Done():
			r.closeConn()
			return
		case <-time.After(delay):
		}
	}
}

// reopen 结果类别，决定重连间隔。
type reopen int

const (
	reopenEOF     reopen = iota // 写端全部关闭
	reopenMissing               // 文件不存在
)

// readOnce 打开 pipe 并读到 EOF/EAGAIN 退出，返回重开类别。
func (r *Reader) readOnce(ctx context.Context) (reopen, error) {
	path := r.pathNow()
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		r.closeConn()
		r.setErr(err.Error())
		if errors.Is(err, os.ErrNotExist) {
			return reopenMissing, err
		}
		return reopenEOF, err
	}
	r.setConn(f)
	defer r.closeConn()

	// 注意：不能用 bufio.Reader——它缓存 EOF，写端关闭一次后
	// 即使新写端接入也永远读不到数据。这里用原生 fd.Read 自管缓冲。
	// 帧定界：一条 item 以 "</item>" 结尾（3.3.8 单行输出、5.x 多行
	// 输出均适用），按帧提取完整 XML 交给解析器。
	buf := make([]byte, 64<<10)
	frameEnd := []byte("</item>")
	var pending []byte // 未完成的帧
	discarding := false
	eofStreak := 0     // 连续无写端次数（>0 说明写端曾关闭）
	for {
		n, err := f.Read(buf)
		if n > 0 {
			eofStreak = 0
			pending = append(pending, buf[:n]...)
			for {
				idx := bytes.Index(pending, frameEnd)
				if idx < 0 {
					break
				}
				end := idx + len(frameEnd)
				if !discarding {
					r.handleItem(ctx, pending[:end])
				}
				discarding = false
				pending = pending[end:]
			}
			if !discarding && len(pending) > maxLine {
				discarding = true // 超长帧（封面等）整体丢弃，直至下一帧尾
				pending = pending[:0]
			}
		}
		switch {
		case err == nil:
			continue
		case errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK):
			if !discarding && len(pending) > maxLine {
				discarding = true
				pending = pending[:0]
			}
			if eofStreak > 0 {
				// 写端暂闭阶段：慢轮询等待写端接入，降低 CPU
				select {
				case <-ctx.Done():
					return reopenEOF, nil
				case <-time.After(500 * time.Millisecond):
				}
			} else {
				select {
				case <-ctx.Done():
					return reopenEOF, nil
				case <-time.After(100 * time.Millisecond):
				}
			}
		case err == io.EOF:
			// 写端全部关闭（shairport 停止或重启中）。
			// 关键：读端保持打开等待新写端，否则 open→read(EOF)→close
			// 的微秒级窗口会让阻塞 open 的写端永远接不进来。
			if !discarding && len(pending) > 0 {
				r.handleItem(ctx, pending)
			}
			pending = pending[:0]
			discarding = false
			eofStreak++
			if eofStreak >= 6 {
				// 约 3s 无写端：重开 fd，感知 pipe 文件被删除重建
				return reopenEOF, nil
			}
			select {
			case <-ctx.Done():
				return reopenEOF, nil
			case <-time.After(500 * time.Millisecond):
			}
		default:
			return reopenEOF, err
		}
	}
}

// handleItem 解析并缓存一条完整 XML 帧。
func (r *Reader) handleItem(_ context.Context, frame []byte) {
	it, err := ParseItem(frame)
	if err != nil {
		return // 解析失败的帧静默丢弃
	}
	r.mu.Lock()
	r.lastItemAt = it.UpdatedAt
	r.mu.Unlock()
	r.cache.Put(it)
}

func (r *Reader) pathNow() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.path
}

func (r *Reader) setConn(f *os.File) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.conn = f
	r.openAt = time.Now()
}

func (r *Reader) closeConn() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.conn != nil {
		r.conn.Close()
		r.conn = nil
	}
}

func (r *Reader) setErr(msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastErr = msg
}

func (r *Reader) clearErr() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastErr = ""
}
