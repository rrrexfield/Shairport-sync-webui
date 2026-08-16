package pipe

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Item 是 metadata pipe 中解析出的一条元数据。
type Item struct {
	Type      string    // 4 字节码 ASCII："core" / "ssnc"
	Code      string    // 4 字节码 ASCII："minm" / "asar" / "asfm" / "PICT" ...
	Data      []byte    // base64 解码后的原始数据
	Truncated bool      // 超过缓存上限被截断（只记录长度）
	Length    int       // 原始数据长度（截断前）
	UpdatedAt time.Time // 缓存时间
}

// Snapshot 是 /api/status 需要的字段级快照。
type Snapshot struct {
	Title      string `json:"title"`
	Artist     string `json:"artist"`
	Album      string `json:"album"`
	Genre      string `json:"genre"`
	Format     string `json:"format"` // asfm，如 "44100/16"
	SenderName string `json:"sender_name"` // snam，播放端设备名
	UpdatedAt  int64  `json:"updated_at"`
}

// Cache 是字段级元数据缓存。白名单外的 code（如封面 PICT）直接丢弃，
// 300MB 内存设备上不缓存大字段。
type Cache struct {
	mu      sync.RWMutex
	items   map[string]Item // "type.code" -> item
	coverAt time.Time       // 最近一次封面出现时间（仅记录，不存数据）
}

// maxFieldData 单字段缓存上限，超出的截断为长度信息。
const maxFieldData = 4 * 1024

// 白名单：webui 展示用到的码。
var keepCodes = map[string]bool{
	"core.minm": true, // title
	"core.asar": true, // artist
	"core.asal": true, // album
	"core.asgn": true, // genre
	"core.asfm": true, // format（源音质）
	"ssnc.snam": true, // 播放端设备名
}

// NewCache 创建缓存。
func NewCache() *Cache {
	return &Cache{items: make(map[string]Item)}
}

// Put 写入一条元数据。
func (c *Cache) Put(it Item) {
	c.mu.Lock()
	defer c.mu.Unlock()
	k := it.Type + "." + it.Code
	if it.Code == "PICT" {
		c.coverAt = it.UpdatedAt
		return
	}
	if !keepCodes[k] {
		return
	}
	if len(it.Data) > maxFieldData {
		it.Data = it.Data[:maxFieldData]
		it.Truncated = true
	}
	it.Length = len(it.Data)
	c.items[k] = it
}

// Snapshot 返回字段级快照（无锁读）。
func (c *Cache) Snapshot() Snapshot {
	c.mu.RLock()
	defer c.mu.RUnlock()
	get := func(k string) string {
		if it, ok := c.items[k]; ok {
			return string(it.Data)
		}
		return ""
	}
	s := Snapshot{
		Title:      get("core.minm"),
		Artist:     get("core.asar"),
		Album:      get("core.asal"),
		Genre:      get("core.asgn"),
		Format:     get("core.asfm"),
		SenderName: get("ssnc.snam"),
	}
	if it, ok := c.items["core.minm"]; ok {
		s.UpdatedAt = it.UpdatedAt.Unix()
	}
	return s
}

// xmlItem 是 pipe 一行的 XML 结构。
// 3.3.8 输出形如：
//
//	<item><type>636f7265</type><code>6d696e6d</code><length>14</length>
//	<data encoding="base64">U2hhaXJwb3J0IFN5bmM=</data></item>
type xmlItem struct {
	XMLName xml.Name `xml:"item"`
	Type    string   `xml:"type"`
	Code    string   `xml:"code"`
	Length  int      `xml:"length"`
	Data    string   `xml:"data"`
}

// ParseItem 解析 pipe 的一行 XML。
func ParseItem(line []byte) (Item, error) {
	var x xmlItem
	if err := xml.Unmarshal(line, &x); err != nil {
		return Item{}, err
	}
	if x.Type == "" || x.Code == "" {
		return Item{}, fmt.Errorf("缺少 type/code")
	}
	typ, err := decodeCode(x.Type)
	if err != nil {
		return Item{}, err
	}
	code, err := decodeCode(x.Code)
	if err != nil {
		return Item{}, err
	}
	// 5.x 的 base64 数据每 76 字符换行一次，需去掉全部空白再解码
	clean := strings.Map(func(r rune) rune {
		switch r {
		case ' ', '\t', '\n', '\r':
			return -1
		}
		return r
	}, x.Data)
	data, err := base64.StdEncoding.DecodeString(clean)
	if err != nil {
		return Item{}, fmt.Errorf("base64 解码失败: %w", err)
	}
	return Item{
		Type:      typ,
		Code:      code,
		Data:      data,
		Length:    x.Length,
		UpdatedAt: time.Now(),
	}, nil
}

// decodeCode 把 4 字节码的 hex 文本转 ASCII（右端去 NUL/空白）。
func decodeCode(s string) (string, error) {
	b, err := hex.DecodeString(strings.TrimSpace(s))
	if err != nil {
		return "", fmt.Errorf("码 %q 非 hex: %w", s, err)
	}
	return strings.TrimRight(string(b), "\x00 \t"), nil
}
