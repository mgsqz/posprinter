package main
 
import (
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
	LatestFile  string `json:"latest_file"`
	BackupDir   string `json:"backup_dir"`
	ListenPort  int    `json:"listen_port"`
	WebPort     int    `json:"web_port"`
	MinDataSize int    `json:"min_data_size"`
}
 
var appConfig = Config{
	LatestFile:  "latest.prn",
	BackupDir:   "./backups",
	ListenPort:  9100,
	WebPort:     8080,
	MinDataSize: 10,
}
 
const bufferSize = 64 * 1024
 
// === SSE 广播器实现 ===
type Broker struct {
	clients map[chan string]bool
	mutex   sync.Mutex
}
 
var broker = NewBroker()
 
func NewBroker() *Broker {
	return &Broker{clients: make(map[chan string]bool)}
}
 
func (b *Broker) AddClient() chan string {
	ch := make(chan string, 10)
	b.mutex.Lock()
	b.clients[ch] = true
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
	if appConfig.MinDataSize <= 0 { appConfig.MinDataSize = 10 }
}
 
func handleConnection(conn net.Conn) {
	defer conn.Close()
	clientAddr := conn.RemoteAddr().String()
 
	initialBuf := make([]byte, appConfig.MinDataSize)
	n, err := io.ReadFull(conn, initialBuf)
	if n < appConfig.MinDataSize {
		log.Printf("丢弃无效连接: 来自 %s (仅 %d 字节)", clientAddr, n)
		return
	}
 
	log.Printf("收到来自 %s 的打印数据，验证通过...", clientAddr)
 
	latestFile, err := os.Create(appConfig.LatestFile)
	if err != nil { return }
	defer latestFile.Close()
 
	timestamp := time.Now().Format("20060102_150405")
	backupFile, err := os.Create(filepath.Join(appConfig.BackupDir, fmt.Sprintf("%s.raw", timestamp)))
	if err != nil { return }
	defer backupFile.Close()
 
	var dataBuf bytes.Buffer
	writer := io.MultiWriter(latestFile, backupFile, &dataBuf)
	writer.Write(initialBuf)
 
	remainingBytes, err := io.CopyBuffer(writer, conn, make([]byte, bufferSize))
	if err != nil {
		log.Printf("数据传输异常 (来自 %s): %v", clientAddr, err)
	}
 
	totalBytes := int64(n) + remainingBytes
	log.Printf("来自 %s 接收完成，共 %d 字节", clientAddr, totalBytes)
 
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
	justification int // 0:左, 1:中, 2:右
 
	qrData []byte
}
 
func (p *parserState) getStyle() string {
	style := ""
	if p.bold { style += "font-weight:bold;" }
	if p.underline { style += "text-decoration:underline;" }
	
	if p.justification == 1 { style += "text-align:center;" } else if p.justification == 2 { style += "text-align:right;" } else { style += "text-align:left;" }
	
	if p.widthMultiple > 1 || p.heightMultiple > 1 {
		fontSize := 14 * p.widthMultiple
		lineHeight := 100 * p.heightMultiple
		style += fmt.Sprintf("font-size:%dpx; line-height:%d%%;", fontSize, lineHeight)
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
	// 核心修复：自动检测编码，如果已经是 UTF-8 则直接使用，避免 GBK 解码器破坏控制字符
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
 
		if b == 27 { // ESC (0x1b)
			if i+1 < len(utf8Data) {
				cmd := utf8Data[i+1]
				switch cmd {
				case 64: // ESC @ 初始化
					p.bold = false; p.underline = false; p.widthMultiple = 1; p.heightMultiple = 1; p.justification = 0
					i += 2; continue
				case 97: // ESC a n 对齐
					if i+2 < len(utf8Data) { p.justification = int(utf8Data[i+2]); i += 3; continue }
				case 69: // ESC E n 加粗
					if i+2 < len(utf8Data) { p.bold = utf8Data[i+2] == 1; i += 3; continue }
				case 45: // ESC - n 下划线
					if i+2 < len(utf8Data) { p.underline = utf8Data[i+2] == 1; i += 3; continue }
				case 33: // ESC ! n 英文字体模式
					if i+2 < len(utf8Data) {
						n := utf8Data[i+2]
						p.bold = (n & 8) != 0
						p.underline = (n & 128) != 0
						p.heightMultiple = 1; p.widthMultiple = 1
						if (n & 16) != 0 { p.heightMultiple = 2 }
						if (n & 32) != 0 { p.widthMultiple = 2 }
						i += 3; continue
					}
				case 100: // ESC d n 走纸n行
					p.flushLine()
					if i+2 < len(utf8Data) {
						for j := 0; j < int(utf8Data[i+2]); j++ { p.sb.WriteString("<br>") }
						i += 3; continue
					}
				case 50: // ESC 2
					i += 2; continue
				case 51, 74, 77, 82, 85, 86, 99, 103, 123, 32: // ESC 3 n 等带1参数
					if i+2 < len(utf8Data) { i += 3; continue }
				}
				i += 2; continue // 未知ESC安全跳过2字节
			}
			i++; continue
		} else if b == 29 { // GS (0x1d)
			if i+1 < len(utf8Data) {
				cmd := utf8Data[i+1]
				if cmd == 40 && i+4 < len(utf8Data) && utf8Data[i+2] == 107 { // GS ( k (QR码)
					pL := int(utf8Data[i+3])
					pH := int(utf8Data[i+4])
					lenPayload := pH*256 + pL
					start := i + 5
					end := start + lenPayload - 2
					if end <= len(utf8Data) {
						cn := utf8Data[start]
						fn := utf8Data[start+1]
						if cn == 49 { // QR Code
							payload := utf8Data[start+2 : end]
							switch fn {
							case 80: // 存储数据
								if len(payload) > 0 { p.qrData = append(p.qrData, payload[1:]...) }
							case 81: // 打印QR码
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
					case 33: // GS ! n 英文字体大小
						if i+2 < len(utf8Data) {
							n := utf8Data[i+2]
							p.widthMultiple = int(n>>4)&0x0F + 1
							p.heightMultiple = int(n)&0x0F + 1
							i += 3; continue
						}
					case 86: // GS V 切纸
						p.flushLine()
						p.sb.WriteString(`<hr style="border-top: 1px dashed #000; margin: 10px 0;">`)
						if i+2 < len(utf8Data) { i += 3; continue }
						i += 2; continue
					case 72, 81, 102, 104, 119, 66: // GS H n 等带1参数
						if i+2 < len(utf8Data) { i += 3; continue }
					case 76, 87: // GS L nL nH 带2参数
						if i+3 < len(utf8Data) { i += 4; continue }
					case 50: // GS 2
						i += 2; continue
					}
					i += 2; continue
				}
			}
			i++; continue
		} else if b == 28 { // FS (0x1c) 指令 (汉字处理)
			if i+1 < len(utf8Data) {
				cmd := utf8Data[i+1]
				switch cmd {
				case 33: // FS ! n 汉字字体模式
					if i+2 < len(utf8Data) {
						n := utf8Data[i+2]
						p.bold = (n & 16) != 0
						p.underline = (n & 32) != 0
						p.widthMultiple = int(n&0x03) + 1
						p.heightMultiple = int((n>>2)&0x03) + 1
						i += 3; continue
					}
				case 38, 46: // FS & , FS .
					i += 2; continue
				case 83: // FS S n1 n2
					if i+3 < len(utf8Data) { i += 4; continue }
				case 67, 73, 74, 99, 110, 45: // FS C n 等
					if i+2 < len(utf8Data) { i += 3; continue }
				}
				i += 2; continue
			}
			i++; continue
		} else if b == 10 { // LF 换行
			p.flushLine()
			i++; continue
		} else if b == 13 { // CR 回车
			i++; continue
		} else if b < 32 { // 其他控制字符丢弃
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
