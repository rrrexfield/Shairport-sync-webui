package pipe

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// itemLine 构造一条 XML item 帧（3.3.8 单行格式）。
func itemLine(typ, code, data string) string {
	th := []byte(typ)
	for len(th) < 4 {
		th = append(th, 0)
	}
	ch := []byte(code)
	for len(ch) < 4 {
		ch = append(ch, 0)
	}
	hexType := ""
	hexCode := ""
	for _, b := range th {
		hexType += string("0123456789abcdef"[b>>4]) + string("0123456789abcdef"[b&0xf])
	}
	for _, b := range ch {
		hexCode += string("0123456789abcdef"[b>>4]) + string("0123456789abcdef"[b&0xf])
	}
	b64 := base64.StdEncoding.EncodeToString([]byte(data))
	return "<item><type>" + hexType + "</type><code>" + hexCode + "</code>" +
		"<length>" + itoa(len(data)) + "</length>" +
		"<data encoding=\"base64\">" + b64 + "</data></item>"
}

// itemFrame52 构造 5.2.1 风格的多行 XML 帧（base64 每 76 字符换行）。
func itemFrame52(typ, code, data string) string {
	s := itemLine(typ, code, data)
	// 把 data 元素的内容换成多行形式：<data ...>\n<76字符块>\n</data>
	b64 := base64.StdEncoding.EncodeToString([]byte(data))
	var wrapped string
	for len(b64) > 76 {
		wrapped += b64[:76] + "\n"
		b64 = b64[76:]
	}
	wrapped += b64
	s = strings.Replace(s, "<data encoding=\"base64\">"+base64.StdEncoding.EncodeToString([]byte(data))+"</data>",
		"<data encoding=\"base64\">\n"+wrapped+"\n</data>", 1)
	// 5.2.1 还在 type/code/length 之后换行
	s = strings.Replace(s, "<data", "\n<data", 1)
	return s
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestParseItem(t *testing.T) {
	line := itemLine("core", "minm", "测试歌曲")
	it, err := ParseItem([]byte(line))
	if err != nil {
		t.Fatal(err)
	}
	if it.Type != "core" || it.Code != "minm" {
		t.Errorf("type/code 解码错误: %q/%q", it.Type, it.Code)
	}
	if string(it.Data) != "测试歌曲" {
		t.Errorf("data = %q", it.Data)
	}
	if it.Length != len("测试歌曲") {
		t.Errorf("length = %d", it.Length)
	}
}

// 5.2.1 的多行帧格式：base64 中间换行、data 标签前后换行，必须正确解析。
func TestParseItemMultiline52(t *testing.T) {
	frame := itemFrame52("core", "minm", strings.Repeat("多行数据测试", 40)) // >76 字符触发换行
	it, err := ParseItem([]byte(frame))
	if err != nil {
		t.Fatal(err)
	}
	if it.Type != "core" || it.Code != "minm" {
		t.Errorf("type/code = %q/%q", it.Type, it.Code)
	}
	if string(it.Data) != strings.Repeat("多行数据测试", 40) {
		t.Errorf("多行 base64 解码错误: %d 字节", len(it.Data))
	}
}

func TestParseItemErrors(t *testing.T) {
	for _, bad := range []string{
		"not xml at all",
		"<item><type>zz</type></item>",          // 非 hex 码
		"<item><type>636f7265</type></item>",     // 缺 code
		"<item><type>636f7265</type><code>6d696e6d</code><data encoding=\"base64\">!!not-base64!!</data></item>",
	} {
		if _, err := ParseItem([]byte(bad)); err == nil {
			t.Errorf("应解析失败: %q", bad)
		}
	}
}

func TestCacheWhitelistAndTruncate(t *testing.T) {
	c := NewCache()
	c.Put(Item{Type: "core", Code: "minm", Data: []byte("标题"), UpdatedAt: time.Now()})
	c.Put(Item{Type: "core", Code: "asar", Data: []byte("歌手"), UpdatedAt: time.Now()})
	c.Put(Item{Type: "core", Code: "asfm", Data: []byte("44100/16"), UpdatedAt: time.Now()})
	c.Put(Item{Type: "ssnc", Code: "snam", Data: []byte("iPhone"), UpdatedAt: time.Now()})
	// 封面：不缓存数据
	c.Put(Item{Type: "ssnc", Code: "PICT", Data: []byte(strings.Repeat("x", 10000)), UpdatedAt: time.Now()})
	// 白名单外：丢弃
	c.Put(Item{Type: "ssnc", Code: "pbeg", Data: []byte("start"), UpdatedAt: time.Now()})
	// 超长字段：截断
	big := strings.Repeat("y", 10*1024)
	c.Put(Item{Type: "core", Code: "asal", Data: []byte(big), UpdatedAt: time.Now()})

	s := c.Snapshot()
	if s.Title != "标题" || s.Artist != "歌手" || s.Format != "44100/16" || s.SenderName != "iPhone" {
		t.Errorf("Snapshot = %+v", s)
	}
	if len(s.Album) > maxFieldData {
		t.Errorf("Album 未截断: %d", len(s.Album))
	}
	if s.UpdatedAt == 0 {
		t.Error("UpdatedAt 未设置")
	}
}

// 完整行读取：写端写几行后关闭，readOnce 读到 EOF 退出。
// 注意时序：必须先让写端进入阻塞 open，否则读端 open→read(EOF)→close
// 一气呵成后写端才被调度，将永久阻塞（FIFO 无写端时读端 read 返回 EOF）。
func TestReadOnceEOF(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "meta")
	if err := syscall.Mkfifo(fifo, 0666); err != nil {
		t.Skipf("无法创建 FIFO: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w, err := os.OpenFile(fifo, os.O_WRONLY, 0)
		if err != nil {
			return
		}
		// 混用 3.3.8 单行帧与 5.2.1 多行帧，帧式读取都应正确
		w.WriteString(itemLine("core", "minm", "测试"))
		w.WriteString(itemFrame52("core", "asfm", "44100/16"))
		w.WriteString(itemFrame52("core", "asar", strings.Repeat("多行艺人名测试", 30)))
		w.Close()
	}()
	time.Sleep(50 * time.Millisecond) // 让写端进入阻塞 open

	r := New(fifo)
	kind, err := r.readOnce(context.Background())
	if err != nil || kind != reopenEOF {
		t.Fatalf("readOnce: kind=%d err=%v", kind, err)
	}
	<-done
	s := r.Snapshot()
	if s.Title != "测试" || s.Format != "44100/16" {
		t.Errorf("Snapshot = %+v", s)
	}
	if s.Artist != strings.Repeat("多行艺人名测试", 30) {
		t.Errorf("多行帧解析错误: %d 字节", len(s.Artist))
	}
	if r.State().Open {
		t.Error("读完后连接应已关闭")
	}
}

// 无写端时非阻塞打开立即成功；读不到数据直到 ctx 取消。
func TestReadOnceNoWriter(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "meta")
	if err := syscall.Mkfifo(fifo, 0666); err != nil {
		t.Skipf("无法创建 FIFO: %v", err)
	}
	r := New(fifo)
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	kind, _ := r.readOnce(ctx)
	if kind != reopenEOF {
		t.Errorf("kind = %d", kind)
	}
}

// 文件不存在：返回 reopenMissing 与错误。
func TestReadOnceMissing(t *testing.T) {
	r := New("/nonexistent/pipe/meta")
	kind, err := r.readOnce(context.Background())
	if kind != reopenMissing || err == nil {
		t.Errorf("kind=%d err=%v", kind, err)
	}
	if !strings.Contains(r.State().Error, "nonexistent") {
		t.Errorf("Error 未记录: %q", r.State().Error)
	}
}

// 超长行（模拟封面）应被整体丢弃，不影响后续行。
func TestOversizeLineDiscarded(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "meta")
	if err := syscall.Mkfifo(fifo, 0666); err != nil {
		t.Skipf("无法创建 FIFO: %v", err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		w, _ := os.OpenFile(fifo, os.O_WRONLY, 0)
		defer w.Close()
		// 超长垃圾行（无 \n 前先写 1MB+ 数据）
		w.WriteString(itemLine("ssnc", "PICT", strings.Repeat("z", maxLine)))
		w.WriteString("\n")
		w.WriteString(itemLine("core", "minm", "超长行之后") + "\n")
		w.Close()
	}()
	time.Sleep(50 * time.Millisecond) // 让写端进入阻塞 open

	r := New(fifo)
	kind, err := r.readOnce(context.Background())
	if err != nil || kind != reopenEOF {
		t.Fatalf("readOnce: kind=%d err=%v", kind, err)
	}
	<-done
	s := r.Snapshot()
	if s.Title != "超长行之后" {
		t.Errorf("超长行丢弃后后续行应正常: %+v", s)
	}
}
