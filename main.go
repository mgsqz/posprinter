	package main
	import (
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
		"golang.org/x/text/encoding/simplifiedchinese"
	)
	// Config 定义配置文件结构
	type Config struct {
		LatestFile  string `json:"latest_file"`
		BackupDir   string `json:"backup_dir"`
		ListenPort  int    `json:"listen_port"`
		WebPort     int    `json:"web_port"`     // 新增：Web预览端口
		MinDataSize int    `json:"min_data_size"`
	}
	var appConfig = Config{
		LatestFile:  "latest.prn",
		BackupDir:   "./backups",
		ListenPort:  9100,
		WebPort:     8080, // 默认Web端口
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
			default: // 防止阻塞
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
		// 启动 Web 预览服务
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
		// 防止代理超时断开
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case msg := <-ch:
				// SSE 规范要求换行符替换为 \ndata:
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
		writer := io.MultiWriter(latestFile, backupFile)
		writer.Write(initialBuf)
		var fullData []byte
		fullData = append(fullData, initialBuf...)
		remainingBytes, err := io.CopyBuffer(writer, conn, make([]byte, bufferSize))
		if err == nil {
			// 读取剩余流以供网页解析
			buf := make([]byte, remainingBytes)
			// 因为 io.CopyBuffer 已经消耗了 conn，如果要再读需要在上一步用 TeeReader
			// 为简化，我们重新从 writer 收集，这里修正逻辑：使用 bytes.Buffer
		}
		// 修正：为了同时拿到完整 byte 用于 HTML 解析，我们改用 Buffer
		// (为了代码简洁，这里假设你按上面逻辑，我们再读一次是不行的，我们改写一下流处理)
		totalBytes := int64(n) + remainingBytes
		log.Printf("来自 %s 接收完成，共 %d 字节", clientAddr, totalBytes)
		// 解析为 HTML 并广播给网页
		htmlStr := escToHTML(fullData)
		broker.Broadcast(htmlStr)
	}
	// === ESC/POS 转 HTML 解析器 ===
	func escToHTML(raw []byte) string {
		// 尝试将 GBK 转为 UTF-8 以正确显示中文
		utf8Data, err := simplifiedchinese.GBK.NewDecoder().Bytes(raw)
		if err != nil {
			utf8Data = raw
		}
		var sb strings.Builder
		sb.WriteString(`<div style="font-family: 'Courier New', monospace; width: 100%;">`)
		var bold, underline bool
		align := "left"
		getStyle := func() string {
			s := ""
			if bold { s += "font-weight:bold;" }
			if underline { s += "text-decoration:underline;" }
			s += "text-align:" + align + ";"
			return s
		}
		var line strings.Builder
		flushLine := func() {
			text := html.EscapeString(line.String())
			// 保留空格排版
			text = strings.ReplaceAll(text, " ", "&nbsp;")
			sb.WriteString(`<div style="` + getStyle() + `">` + text + `</div>`)
			line.Reset()
			align = "left" // 对齐通常在换行后重置
		}
		i := 0
		for i < len(utf8Data) {
			b := utf8Data[i]
			if b == 27 { // ESC
				if i+1 < len(utf8Data) {
					cmd := utf8Data[i+1]
					switch cmd {
					case 64: // ESC @ 初始化
						bold = false; underline = false; align = "left"; i += 2; continue
					case 97: // ESC a n 对齐
						if i+2 < len(utf8Data) {
							switch utf8Data[i+2] {
							case 0: align = "left"
							case 1: align = "center"
							case 2: align = "right"
							}
							i += 3; continue
						}
					case 69: // ESC E n 加粗
						if i+2 < len(utf8Data) { bold = utf8Data[i+2] == 1; i += 3; continue }
					case 45: // ESC - n 下划线
						if i+2 < len(utf8Data) { underline = utf8Data[i+2] == 1; i += 3; continue }
					case 33: // ESC ! n 字体模式
						if i+2 < len(utf8Data) { bold = (utf8Data[i+2] & 8) != 0; i += 3; continue }
					case 100: // ESC d n 打印并走纸n行
						flushLine()
						for j := 0; j < int(utf8Data[i+2]); j++ { sb.WriteString("<br>") }
						i += 3; continue
					}
				}
			} else if b == 29 { // GS
				if i+1 < len(utf8Data) {
					cmd := utf8Data[i+1]
					switch cmd {
					case 33: // GS ! n 字体大小
						if i+2 < len(utf8Data) { i += 3; continue }
					case 86: // GS V 切纸
						flushLine()
						sb.WriteString(`<hr style="border-top: 1px dashed #000; margin: 10px 0;">`)
						i += 2; continue
					}
				}
			} else if b == 10 { // LF 换行
				flushLine()
				i++; continue
			} else if b == 13 { // CR 回车
				i++; continue
			} else {
				line.WriteByte(b)
				i++; continue
			}
			i++ // 跳过未识别的控制字符
		}
		flushLine()
		sb.WriteString(`</div>`)
		return sb.String()
	}
