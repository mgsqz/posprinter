package main
 
import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
 
	"github.com/skip2/go-qrcode"
	"golang.org/x/text/encoding/simplifiedchinese"
)
 
// Config 定义配置文件结构
type Config struct {
	LatestFile string `json:"latest_file"`
	BackupDir  string `json:"backup_dir"`
	ListenPort int    `json:"listen_port"`
	WebPort    int    `json:"web_port"`
}
 
var appConfig = Config{
	LatestFile: "latest.prn",
	BackupDir:  "./backups",
	ListenPort: 9100,
	WebPort:    8080,
}
 
// === SSE 广播器实现 ===
type Broker struct {
	clients     map[chan string]bool
	mutex       sync.Mutex
	lastMessage string
}
 
var broker = NewBroker()
 
func NewBroker() *Broker {
	return &Broker{clients: make(map[chan string]bool)}
}
 
func (b *Broker) AddClient() chan string {
	ch := make(chan string, 10)
	b.mutex.Lock()
	b.clients[ch] = true
	if b.lastMessage != "" {
		ch <- b.lastMessage
	}
	b.mutex.Unlock()
	return ch
}
 
func (b *Broker) RemoveClient(ch chan string) {
	b.mutex.Lock()
	delete(b.clients, ch)
	close(ch)
	b.mutex.Unlock()
}
 
func (b *Broker) Broadcast(msg string) {
	b.mutex.Lock()
	b.lastMessage = msg
	for ch := range b.clients {
		select {
		case ch <- msg:
		default:
		}
	}
	b.mutex.Unlock()
}
 
func main() {
	configPath := flag.String("c", "config.json", "指定配置文件路径")
	flag.Parse()
 
	loadConfig(*configPath)
 
	if err := os.MkdirAll(appConfig.BackupDir, 0755); err != nil {
		log.Fatalf("无法创建备份目录: %v", err)
	}
 
	latestFileDir := filepath.Dir(appConfig.LatestFile)
	if latestFileDir != "." && latestFileDir != "" {
		if err := os.MkdirAll(latestFileDir, 0755); err != nil {
			log.Fatalf("无法创建主文件目录: %v", err)
		}
	}
 
	go startWebServer()
 
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", appConfig.ListenPort))
	if err != nil {
		log.Fatalf("监听端口 %d 失败: %v", appConfig.ListenPort, err)
	}
	defer listener.Close()
 
	log.Printf("ESC/POS RAW 接收服务已启动，监听端口: %d", appConfig.ListenPort)
	log.Printf("Web 预览服务已启动: http://localhost:%d", appConfig.WebPort)
 
	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn)
	}
}
 
func startWebServer() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/events", sseHandler)
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appConfig.WebPort), nil))
}
 
func indexHandler(w http.ResponseWriter, r *http.Request) {
	htmlContent := `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <title>ESC/POS 实时小票预览</title>
    <style>
        body { background-color: #f0f0f0; font-family: sans-serif; text-align: center; padding: 20px; }
        .receipt-container { background: #fff; padding: 20px; box-shadow: 0 0 15px rgba(0,0,0,0.1); display: inline-block; min-height: 500px; width: 350px; }
        #receipt { font-family: 'Courier New', monospace; font-size: 14px; text-align: left; }
        .status { color: #888; margin-bottom: 10px; }
    </style>
</head>
<body>
    <div class="status" id="status">等待打印数据...</div>
    <div class="receipt-container">
        <div id="receipt"></div>
    </div>
    <script>
        const evtSource = new EventSource('/events');
        const receiptDiv = document.getElementById('receipt');
        const statusDiv = document.getElementById('status');
        
        evtSource.onmessage = function(e) {
            receiptDiv.innerHTML = e.data;
            statusDiv.innerText = "最后更新: " + new Date().toLocaleTimeString();
        };
    </script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, htmlContent)
}
 
func sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
 
	ch := broker.AddClient()
	defer broker.RemoveClient(ch)
 
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
 
	for {
		select {
		case msg := <-ch:
			encodedMsg := strings.ReplaceAll(msg, "\n", "\ndata: ")
			fmt.Fprintf(w, "data: %s\n\n", encodedMsg)
			if f, ok := w.(http.Flusher); ok { f.Flush() }
		case <-ticker.C:
			fmt.Fprintf(w, "event: ping\ndata: \n\n")
			if f, ok := w.(http.Flusher); ok { f.Flush() }
		case <-r.Context().Done():
			return
		}
	}
}
 
func loadConfig(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("未找到配置文件 '%s'，将使用默认配置。\n", configPath)
		return
	}
	if err := json.Unmarshal(data, &appConfig); err != nil {
		log.Printf("解析配置文件失败: %v，将使用默认配置。\n", err)
		return
	}
	if appConfig.LatestFile == "" { appConfig.LatestFile = "latest.prn" }
	if appConfig.BackupDir == "" { appConfig.BackupDir = "./backups" }
	if appConfig.ListenPort == 0 { appConfig.ListenPort = 9100 }
	if appConfig.WebPort == 0 { appConfig.WebPort = 8080 }
}
 
func handleConnection(conn net.Conn) {
	defer conn.Close()
	clientAddr := conn.RemoteAddr().String()
 
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
 
	log.Printf("收到来自 %s 的连接，开始监听流...", clientAddr)
 
	// 核心：先缓存到内存，延迟写盘
	var dataBuf bytes.Buffer
	reader := bufio.NewReader(conn)
	totalBytes := 0
 
	for {
		b, err := reader.ReadByte()
		if err != nil {
			break
		}
 
		// === 实时状态指令拦截与回复 ===
		if b == 0x10 { // DLE
			b2, err := reader.ReadByte()
			if err == nil {
				if b2 == 0x04 { // DLE EOT n
					b3, err := reader.ReadByte()
					if err == nil {
						var resp byte
						switch b3 {
						case 1: resp = 0x16
						case 2: resp = 0x12
						case 3: resp = 0x00
						case 4: resp = 0x12
						}
						conn.Write([]byte{resp})
						continue
					}
				}
				dataBuf.WriteByte(b)
				dataBuf.WriteByte(b2)
				totalBytes += 2
				continue
			}
		} else if b == 0x1d { // GS
			b2, err := reader.ReadByte()
			if err == nil {
				if b2 == 0x72 { // GS r n
					b3, err := reader.ReadByte()
					if err == nil {
						if b3 == 1 || b3 == 2 || b3 == 4 {
							conn.Write([]byte{0x00})
							continue
						}
						dataBuf.WriteByte(b)
						dataBuf.WriteByte(b2)
						dataBuf.WriteByte(b3)
						totalBytes += 3
						continue
					}
				}
				dataBuf.WriteByte(b)
				dataBuf.WriteByte(b2)
				totalBytes += 2
				continue
			}
		} else if b == 0x1b { // ESC
			b2, err := reader.ReadByte()
			if err == nil {
				if b2 == 0x76 { // ESC v
					conn.Write([]byte{0x00})
					continue
				} else if b2 == 0x75 { // ESC u n
					if _, err := reader.ReadByte(); err == nil {
						conn.Write([]byte{0x00})
						continue
					}
				}
				dataBuf.WriteByte(b)
				dataBuf.WriteByte(b2)
				totalBytes += 2
				continue
			}
		}
 
		dataBuf.WriteByte(b)
		totalBytes++
	}
 
	// 判断是否为有效的打印数据
	isValidPrint := false
	if totalBytes >= 10 && bytes.Contains(dataBuf.Bytes(), []byte{0x0A}) {
		isValidPrint = true
	} else if totalBytes >= 50 {
		isValidPrint = true
	}
 
	if !isValidPrint {
		log.Printf("丢弃探测/无效数据: 来自 %s (仅 %d 字节)", clientAddr, totalBytes)
		return
	}
 
	log.Printf("来自 %s 接收完成，共 %d 字节，正在写入文件...", clientAddr, totalBytes)
 
	latestFile, err := os.Create(appConfig.LatestFile)
	if err != nil { log.Printf("创建主文件失败: %v", err); return }
	defer latestFile.Close()
 
	timestamp := time.Now().Format("20060102_150405")
	backupFile, err := os.Create(filepath.Join(appConfig.BackupDir, fmt.Sprintf("%s.raw", timestamp)))
	if err != nil { log.Printf("创建备份文件失败: %v", err); return }
	defer backupFile.Close()
 
	latestFile.Write(dataBuf.Bytes())
	backupFile.Write(dataBuf.Bytes())
 
	htmlStr := escToHTML(dataBuf.Bytes())
	broker.Broadcast(htmlStr)
}
 
// === ESC/POS 状态解析器 ===
type parserState struct {
	sb   strings.Builder
	line strings.Builder
 
	bold          bool
	underline     bool
	widthMultiple int
	heightMultiple int
	justification int
 
	qrData []byte
}
 
func (p *parserState) getStyle() string {
	style := ""
	if p.bold { style += "font-weight:bold;" }
	if p.underline { style += "text-decoration:underline;" }
	
	if p.justification == 1 { style += "text-align:center;" } else if p.justification == 2 { style += "text-align:right;" } else { style += "text-align:left;" }
	
	scale := 1
	if p.widthMultiple > p.heightMultiple {
		scale = p.widthMultiple
	} else {
		scale = p.heightMultiple
	}
	if scale > 1 {
		style += fmt.Sprintf("font-size:%dpx; line-height:%d%%;", 14 * scale, 120 * p.heightMultiple)
	}
	return style
}
 
func (p *parserState) flushLine() {
	text := html.EscapeString(p.line.String())
	text = strings.ReplaceAll(text, " ", "&nbsp;")
	p.sb.WriteString(`<div style="` + p.getStyle() + `">` + text + `</div>`)
	p.line.Reset()
}
 
func escToHTML(raw []byte) string {
	var utf8Data []byte
	if utf8.Valid(raw) {
		utf8Data = raw
	} else {
		decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw)
		if err == nil {
			utf8Data = decoded
		} else {
			utf8Data = raw
		}
	}
 
	p := &parserState{
		widthMultiple:  1,
		heightMultiple: 1,
	}
	p.sb.WriteString(`<div style="font-family: 'Courier New', monospace; width: 100%;">`)
 
	i := 0
	for i < len(utf8Data) {
		b := utf8Data[i]
 
		if b == 27 { // ESC
			if i+1 < len(utf8Data) {
				cmd := utf8Data[i+1]
				switch cmd {
				case 64:
					p.bold = false; p.underline = false; p.widthMultiple = 1; p.heightMultiple = 1; p.justification = 0
					i += 2; continue
				case 97:
					if i+2 < len(utf8Data) { p.justification = int(utf8Data[i+2]); i += 3; continue }
				case 69:
					if i+2 < len(utf8Data) { p.bold = utf8Data[i+2] == 1; i += 3; continue }
				case 45:
					if i+2 < len(utf8Data) { p.underline = utf8Data[i+2] == 1; i += 3; continue }
				case 33:
					if i+2 < len(utf8Data) {
						n := utf8Data[i+2]
						p.bold = (n & 8) != 0
						p.underline = (n & 128) != 0
						p.heightMultiple = 1
						if (n & 16) != 0 { p.heightMultiple = 2 }
						p.widthMultiple = 1
						if (n & 32) != 0 { p.widthMultiple = 2 }
						i += 3; continue
					}
				case 100:
					p.flushLine()
					if i+2 < len(utf8Data) {
						for j := 0; j < int(utf8Data[i+2]); j++ { p.sb.WriteString("<br>") }
						i += 3; continue
					}
				case 50:
					i += 2; continue
				case 51, 74, 77, 82, 85, 86, 99, 103, 123, 32:
					if i+2 < len(utf8Data) { i += 3; continue }
				}
				i += 2; continue
			}
			i++; continue
		} else if b == 29 { // GS
			if i+1 < len(utf8Data) {
				cmd := utf8Data[i+1]
				if cmd == 40 && i+4 < len(utf8Data) && utf8Data[i+2] == 107 {
					pL := int(utf8Data[i+3])
					pH := int(utf8Data[i+4])
					lenPayload := pH*256 + pL
					start := i + 5
					end := start + lenPayload - 2
					if end <= len(utf8Data) {
						cn := utf8Data[start]
						fn := utf8Data[start+1]
						if cn == 49 {
							payload := utf8Data[start+2 : end]
							switch fn {
							case 80:
								if len(payload) > 0 { p.qrData = append(p.qrData, payload[1:]...) }
							case 81:
								if len(p.qrData) > 0 {
									png, err := qrcode.Encode(string(p.qrData), qrcode.Medium, 256)
									if err == nil {
										b64 := base64.StdEncoding.EncodeToString(png)
										p.line.WriteString(fmt.Sprintf(`<img src="data:image/png;base64,%s" style="max-width:200px;"/>`, b64))
									}
									p.qrData = []byte{}
								}
							}
						}
						i = end; continue
					}
				} else {
					switch cmd {
					case 33:
						if i+2 < len(utf8Data) {
							n := utf8Data[i+2]
							p.widthMultiple = int((n>>4)&0x0F) + 1
							p.heightMultiple = int(n&0x0F) + 1
							i += 3; continue
						}
					case 86:
						p.flushLine()
						p.sb.WriteString(`<hr style="border-top: 1px dashed #000; margin: 10px 0;">`)
						if i+2 < len(utf8Data) { i += 3; continue }
						i += 2; continue
					case 72, 81, 102, 104, 119, 66:
						if i+2 < len(utf8Data) { i += 3; continue }
					case 76, 87:
						if i+3 < len(utf8Data) { i += 4; continue }
					case 50:
						i += 2; continue
					}
					i += 2; continue
				}
			}
			i++; continue
		} else if b == 28 { // FS
			if i+1 < len(utf8Data) {
				cmd := utf8Data[i+1]
				switch cmd {
				case 33:
					if i+2 < len(utf8Data) {
						n := utf8Data[i+2]
						p.bold = (n & 16) != 0
						p.underline = (n & 32) != 0
						p.heightMultiple = 1
						if (n & 8) != 0 { p.heightMultiple = 2 }
						p.widthMultiple = 1
						if (n & 1) != 0 { p.widthMultiple = 2 }
						i += 3; continue
					}
				case 38, 46:
					i += 2; continue
				case 83:
					if i+3 < len(utf8Data) { i += 4; continue }
				case 67, 73, 74, 99, 110, 45:
					if i+2 < len(utf8Data) { i += 3; continue }
				}
				i += 2; continue
			}
			i++; continue
		} else if b == 10 {
			p.flushLine()
			i++; continue
		} else if b == 13 {
			i++; continue
		} else if b < 32 {
			i++; continue
		} else {
			p.line.WriteByte(b)
			i++; continue
		}
	}
	p.flushLine()
	p.sb.WriteString(`</div>`)
	return p.sb.String()
}
