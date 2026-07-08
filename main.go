package main
 
import (
	"bufio"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"html"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
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
 
// === Web 服务 ===
func startWebServer() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/data", dataHandler)
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
        body { margin: 0; background-color: #f0f0f0; font-family: sans-serif; text-align: center; padding: 20px; }
        .receipt-container { background: #fff; padding: 20px; box-shadow: 0 0 15px rgba(0,0,0,0.1); display: inline-block; min-height: 500px; }
        .status { color: #888; margin-bottom: 10px; }
        
        /* 完全复刻参考项目的 CSS */
        .esc-receipt {
          border: 1px solid #888;
          font-family: monospace;
          padding: 1em;
          width: 80mm; 
          display: inline-block;
          text-align: left;
        }
        .esc-line { white-space: pre; }
        .esc-emphasis { font-weight: bold; }
        .esc-justify-center .esc-text-scaled { transform-origin: 50% 0; }
        .esc-justify-right .esc-text-scaled { transform-origin: 100% 0; }
        .esc-justify-center { text-align: center; }
        .esc-justify-right { text-align: right; }
        .esc-text-scaled { display: inline-block; transform-origin: 0 0; }
        .esc-justify-center .esc-bitimage { margin-left: auto; margin-right: auto; }
        .esc-justify-right .esc-bitimage { margin-left: auto; }
        .esc-underline { border-bottom: 2px solid #000; }
        .esc-underline-double { border-bottom: 2px solid #000; }
        .esc-invert { background: #000; color: #fff; }
        .esc-upside-down { transform: rotate(180deg); }
        .esc-font-b { font-size: 75% }
        .esc-line-command { text-align: center; font-weight: bold; background: linear-gradient(180deg, rgba(0,0,0,0) calc(50% - 1px), rgba(192,192,192,1) calc(50%), rgba(0,0,0,0) calc(50% + 1px)); }
        .esc-line-command .command { background-color: white; padding: 1px 10px 1px 10px; }
        span { display: inline-block; }
        .esc-width-2 { transform: scale(2, 1); }
        .esc-width-3 { transform: scale(3, 1); }
        .esc-width-4 { transform: scale(4, 1); }
        .esc-width-5 { transform: scale(5, 1); }
        .esc-width-6 { transform: scale(6, 1); }
        .esc-width-7 { transform: scale(7, 1); }
        .esc-width-8 { transform: scale(8, 1); }
        .esc-height-2 { transform: scale(1, 2); margin-bottom: 1em; }
        .esc-height-3 { transform: scale(1, 3); margin-bottom: 2em; }
        .esc-height-4 { transform: scale(1, 4); margin-bottom: 3em; }
        .esc-height-5 { transform: scale(1, 5); margin-bottom: 4em; }
        .esc-height-6 { transform: scale(1, 6); margin-bottom: 5em; }
        .esc-height-7 { transform: scale(1, 7); margin-bottom: 6em; }
        .esc-height-8 { transform: scale(1, 8); margin-bottom: 7em; }
        .esc-width-2-height-2 { transform: scale(2, 2); margin-bottom: 1em; }
        .esc-width-2-height-3 { transform: scale(2, 3); margin-bottom: 2em; }
        .esc-width-2-height-4 { transform: scale(2, 4); margin-bottom: 3em; }
        .esc-width-2-height-5 { transform: scale(2, 5); margin-bottom: 4em; }
        .esc-width-2-height-6 { transform: scale(2, 6); margin-bottom: 5em; }
        .esc-width-2-height-7 { transform: scale(2, 7); margin-bottom: 6em; }
        .esc-width-2-height-8 { transform: scale(2, 8); margin-bottom: 7em; }
        .esc-width-3-height-2 { transform: scale(3, 2); margin-bottom: 1em; }
        .esc-width-3-height-3 { transform: scale(3, 3); margin-bottom: 2em; }
        .esc-width-3-height-4 { transform: scale(3, 4); margin-bottom: 3em; }
        .esc-width-3-height-5 { transform: scale(3, 5); margin-bottom: 4em; }
        .esc-width-3-height-6 { transform: scale(3, 6); margin-bottom: 5em; }
        .esc-width-3-height-7 { transform: scale(3, 7); margin-bottom: 6em; }
        .esc-width-3-height-8 { transform: scale(3, 8); margin-bottom: 7em; }
        .esc-width-4-height-2 { transform: scale(4, 2); margin-bottom: 1em; }
        .esc-width-4-height-3 { transform: scale(4, 3); margin-bottom: 2em; }
        .esc-width-4-height-4 { transform: scale(4, 4); margin-bottom: 3em; }
        .esc-width-4-height-5 { transform: scale(4, 5); margin-bottom: 4em; }
        .esc-width-4-height-6 { transform: scale(4, 6); margin-bottom: 5em; }
        .esc-width-4-height-7 { transform: scale(4, 7); margin-bottom: 6em; }
        .esc-width-4-height-8 { transform: scale(4, 8); margin-bottom: 7em; }
        .esc-width-5-height-2 { transform: scale(5, 2); margin-bottom: 1em; }
        .esc-width-5-height-3 { transform: scale(5, 3); margin-bottom: 2em; }
        .esc-width-5-height-4 { transform: scale(5, 4); margin-bottom: 3em; }
        .esc-width-5-height-5 { transform: scale(5, 5); margin-bottom: 4em; }
        .esc-width-5-height-6 { transform: scale(5, 6); margin-bottom: 5em; }
        .esc-width-5-height-7 { transform: scale(5, 7); margin-bottom: 6em; }
        .esc-width-5-height-8 { transform: scale(5, 8); margin-bottom: 7em; }
        .esc-width-6-height-2 { transform: scale(6, 2); margin-bottom: 1em; }
        .esc-width-6-height-3 { transform: scale(6, 3); margin-bottom: 2em; }
        .esc-width-6-height-4 { transform: scale(6, 4); margin-bottom: 3em; }
        .esc-width-6-height-5 { transform: scale(6, 5); margin-bottom: 4em; }
        .esc-width-6-height-6 { transform: scale(6, 6); margin-bottom: 5em; }
        .esc-width-6-height-7 { transform: scale(6, 7); margin-bottom: 6em; }
        .esc-width-6-height-8 { transform: scale(6, 8); margin-bottom: 7em; }
        .esc-width-7-height-2 { transform: scale(7, 2); margin-bottom: 1em; }
        .esc-width-7-height-3 { transform: scale(7, 3); margin-bottom: 2em; }
        .esc-width-7-height-4 { transform: scale(7, 4); margin-bottom: 3em; }
        .esc-width-7-height-5 { transform: scale(7, 5); margin-bottom: 4em; }
        .esc-width-7-height-6 { transform: scale(7, 6); margin-bottom: 5em; }
        .esc-width-7-height-7 { transform: scale(7, 7); margin-bottom: 6em; }
        .esc-width-7-height-8 { transform: scale(7, 8); margin-bottom: 7em; }
        .esc-width-8-height-2 { transform: scale(8, 2); margin-bottom: 1em; }
        .esc-width-8-height-3 { transform: scale(8, 3); margin-bottom: 2em; }
        .esc-width-8-height-4 { transform: scale(8, 4); margin-bottom: 3em; }
        .esc-width-8-height-5 { transform: scale(8, 5); margin-bottom: 4em; }
        .esc-width-8-height-6 { transform: scale(8, 6); margin-bottom: 5em; }
        .esc-width-8-height-7 { transform: scale(8, 7); margin-bottom: 6em; }
        .esc-width-8-height-8 { transform: scale(8, 8); margin-bottom: 7em; }
        .esc-bitimage { display: block; }
    </style>
</head>
<body>
    <div class="status" id="status">等待打印数据...</div>
    <div class="receipt-container">
        <div id="receipt"></div>
    </div>
    <script>
        async function fetchReceipt() {
            try {
                const response = await fetch('/data?ts=' + new Date().getTime());
                if (response.ok) {
                    const html = await response.text();
                    document.getElementById('receipt').innerHTML = html;
                    document.getElementById('status').innerText = "最后刷新: " + new Date().toLocaleTimeString();
                }
            } catch (e) {}
        }
        setInterval(fetchReceipt, 1000); // 每秒执行一次
        fetchReceipt(); // 首次立即执行
    </script>
</body>
</html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprint(w, htmlContent)
}
 
func dataHandler(w http.ResponseWriter, r *http.Request) {
	rawData, err := os.ReadFile(appConfig.LatestFile)
	if err != nil {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte(`<div style="color:#999;">暂无打印数据</div>`))
		return
	}
	htmlStr := escToHTML(rawData)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(htmlStr))
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
 
// === TCP 接收处理 ===
func handleConnection(conn net.Conn) {
	defer conn.Close()
	clientAddr := conn.RemoteAddr().String()
 
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
 
	log.Printf("收到来自 %s 的连接，开始监听流...", clientAddr)
 
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
}
 
// === ESC/POS 状态解析器 (使用与 PHP 完全一致的 class 机制) ===
type parserState struct {
	sb      strings.Builder
	lineBuf strings.Builder
	textBuf strings.Builder
 
	bold          bool
	underline     bool
	widthMultiple int
	heightMultiple int
	justification int
 
	qrData []byte
}
 
func (p *parserState) flushText() {
	if p.textBuf.Len() > 0 {
		// 限制最大倍数，与 PHP 代码一致
		if p.widthMultiple > 8 { p.widthMultiple = 8 }
		if p.heightMultiple > 8 { p.heightMultiple = 8 }
 
		text := html.EscapeString(p.textBuf.String())
		text = strings.ReplaceAll(text, " ", "&nbsp;")
		
		classes := []string{}
		if p.bold { classes = append(classes, "esc-emphasis") }
		if p.underline { classes = append(classes, "esc-underline") }
		
		if p.widthMultiple > 1 || p.heightMultiple > 1 {
			classes = append(classes, "esc-text-scaled")
			widthClass := ""
			if p.widthMultiple > 1 { widthClass = "-width-" + fmt.Sprintf("%d", p.widthMultiple) }
			heightClass := ""
			if p.heightMultiple > 1 { heightClass = "-height-" + fmt.Sprintf("%d", p.heightMultiple) }
			classes = append(classes, "esc" + widthClass + heightClass)
		}
		
		if len(classes) > 0 {
			p.lineBuf.WriteString(`<span class="` + strings.Join(classes, " ") + `">` + text + `</span>`)
		} else {
			p.lineBuf.WriteString(text)
		}
		p.textBuf.Reset()
	}
}
 
func (p *parserState) flushLine() {
	p.flushText()
	
	classes := []string{"esc-line"}
	if p.justification == 1 { classes = append(classes, "esc-justify-center") } else if p.justification == 2 { classes = append(classes, "esc-justify-right") }
	
	classesStr := strings.Join(classes, " ")
	
	if p.lineBuf.Len() == 0 {
		p.sb.WriteString(`<div class="` + classesStr + `">&nbsp;</div>`)
	} else {
		p.sb.WriteString(`<div class="` + classesStr + `">` + p.lineBuf.String() + `</div>`)
	}
	p.lineBuf.Reset()
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
	p.sb.WriteString(`<div class="esc-receipt">`)
 
	i := 0
	for i < len(utf8Data) {
		b := utf8Data[i]
 
		if b == 27 { // ESC
			p.flushText()
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
			p.flushText()
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
										p.lineBuf.WriteString(fmt.Sprintf(`<img class="esc-bitimage" src="data:image/png;base64,%s" style="max-width:200px;"/>`, b64))
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
			p.flushText()
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
			p.textBuf.WriteByte(b)
			i++; continue
		}
	}
	p.flushLine()
	p.sb.WriteString(`</div>`)
	return p.sb.String()
}
