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
        body { background-color: #f0f0f0; font-family: sans-serif; text-align: center; padding: 20px; margin: 0; }
        /* 去掉 max-width 限制，宽度设为 90%，使其能随浏览器拉宽 */
        .receipt-container { background: #fff; padding: 40px; box-shadow: 0 0 15px rgba(0,0,0,0.1); display: inline-block; min-height: 90vh; width: 90%; }
        .status { color: #888; margin-bottom: 10px; font-size: 20px; }
        
        .esc-receipt {
          font-family: 'Courier New', monospace;
          font-size: 3vw; /* 核心修复：使用 vw 视口单位，页面拉宽时字体会自动等比放大 */
          width: 100%;
          text-align: left;
          display: inline-block;
          line-height: 1.2;
        }
    </style>
</head>
<body>
    <div class="status" id="status">等待打印数据...</div>
    <div class="receipt-container">
        <div id="receipt" class="esc-receipt"></div>
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
 
// === ESC/POS 状态解析器 ===
type parserState struct {
	sb      strings.Builder
	lineBuf strings.Builder
	textBuf []byte
 
	bold          bool
	underline     bool
	widthMultiple int
	heightMultiple int
	justification int
 
	qrData []byte
}
 
func (p *parserState) flushText() {
	if len(p.textBuf) > 0 {
		var utf8Str string
		if utf8.Valid(p.textBuf) {
			utf8Str = string(p.textBuf)
		} else {
			decoded, err := simplifiedchinese.GBK.NewDecoder().Bytes(p.textBuf)
			if err == nil {
				utf8Str = string(decoded)
			} else {
				utf8Str = string(p.textBuf)
			}
		}
		
		text := html.EscapeString(utf8Str)
		text = strings.ReplaceAll(text, " ", "&nbsp;")
		
		style := ""
		if p.bold { style += "font-weight:bold;" }
		if p.underline { style += "text-decoration:underline;" }
		
		if p.widthMultiple > 1 || p.heightMultiple > 1 {
			if p.widthMultiple == p.heightMultiple {
				// 宽高一致：使用 em 单位完美继承父级 3vw 的基础字号，实现响应式放大
				style += fmt.Sprintf("font-size: %dem; display: inline-block; vertical-align: middle;", p.heightMultiple)
			} else if p.widthMultiple == 1 {
				style += "display: inline-block; vertical-align: middle;"
				marginVal := p.heightMultiple - 1
				style += fmt.Sprintf("transform: scaleY(%d); transform-origin: center; margin-bottom: %dem;", p.heightMultiple, marginVal)
			} else {
				style += "display: inline-block; vertical-align: middle;"
				if p.heightMultiple > 1 {
					marginVal := p.heightMultiple - 1
					style += fmt.Sprintf("transform: scaleY(%d); transform-origin: center; margin-bottom: %dem;", p.heightMultiple, marginVal)
				}
				if p.widthMultiple > 1 {
					style += fmt.Sprintf("letter-spacing: %.2fem;", 1.0 * float64(p.widthMultiple - 1))
				}
			}
			p.lineBuf.WriteString(`<span style="` + style + `">` + text + `</span>`)
		} else {
			if style != "" {
				p.lineBuf.WriteString(`<span style="` + style + `">` + text + `</span>`)
			} else {
				p.lineBuf.WriteString(text)
			}
		}
		p.textBuf = p.textBuf[:0]
	}
}
 
func (p *parserState) flushLine() {
	p.flushText()
	
	align := "left"
	if p.justification == 1 { align = "center" } else if p.justification == 2 { align = "right" }
	
	// 核心修复：移除 pre-wrap 防止自动换行，并裁剪行尾的 &nbsp; 空格，防止其干扰居中和居右对齐
	lineContent := strings.TrimRight(p.lineBuf.String(), "&nbsp;")
	divStyle := "white-space: nowrap; text-align:" + align + ";"
	
	if len(lineContent) == 0 {
		p.sb.WriteString(`<div style="` + divStyle + `">&nbsp;</div>`)
	} else {
		p.sb.WriteString(`<div style="` + divStyle + `">` + lineContent + `</div>`)
	}
	p.lineBuf.Reset()
}
 
func escToHTML(raw []byte) string {
	p := &parserState{
		widthMultiple:  1,
		heightMultiple: 1,
	}
 
	i := 0
	for i < len(raw) {
		b := raw[i]
 
		if b == 27 { // ESC
			p.flushText()
			if i+1 < len(raw) {
				cmd := raw[i+1]
				switch cmd {
				case 64: // ESC @ 初始化
					p.bold = false; p.underline = false; p.widthMultiple = 1; p.heightMultiple = 1; p.justification = 0
					i += 2; continue
				case 97: // ESC a n
					if i+2 < len(raw) { p.justification = int(raw[i+2]); i += 3; continue }
				case 69: // ESC E n
					if i+2 < len(raw) { 
						n := raw[i+2]
						p.bold = (n != 0) 
						i += 3; continue 
					}
				case 45: // ESC - n
					if i+2 < len(raw) { p.underline = raw[i+2] == 1; i += 3; continue }
				case 33: // ESC ! n
					if i+2 < len(raw) {
						n := raw[i+2]
						if (n & 8) != 0 { p.bold = true } else if n == 0 { p.bold = false }
						if (n & 128) != 0 { p.underline = true } else if n == 0 { p.underline = false }
						if n == 0 { p.bold = false }
						
						p.heightMultiple = 1
						if (n & 16) != 0 { p.heightMultiple = 2 }
						p.widthMultiple = 1
						if (n & 32) != 0 { p.widthMultiple = 2 }
						i += 3; continue
					}
				case 112: // ESC p m t1 t2 (钱箱开启脉冲，3个参数)
					if i+4 < len(raw) { i += 5; continue }
					i += 2; continue
				case 100: // ESC d n
					p.flushLine()
					if i+2 < len(raw) {
						for j := 0; j < int(raw[i+2]); j++ { p.sb.WriteString("<br>") }
						i += 3; continue
					}
				case 50: // ESC 2
					i += 2; continue
				case 51, 74, 77, 82, 85, 86, 99, 103, 123, 32: // ESC 3 n 等带1参数
					if i+2 < len(raw) { i += 3; continue }
				}
				i += 2; continue
			}
			i++; continue
		} else if b == 29 { // GS
			p.flushText()
			if i+1 < len(raw) {
				cmd := raw[i+1]
				if cmd == 40 && i+4 < len(raw) && raw[i+2] == 107 { // GS ( k (QR码)
					pL := int(raw[i+3])
					pH := int(raw[i+4])
					lenPayload := pH*256 + pL
					start := i + 5
					end := start + lenPayload - 2
					if end <= len(raw) {
						cn := raw[start]
						fn := raw[start+1]
						if cn == 49 {
							payload := raw[start+2 : end]
							switch fn {
							case 80:
								if len(payload) > 0 { p.qrData = append(p.qrData, payload[1:]...) }
							case 81:
								if len(p.qrData) > 0 {
									png, err := qrcode.Encode(string(p.qrData), qrcode.Medium, 256)
									if err == nil {
										b64 := base64.StdEncoding.EncodeToString(png)
										p.lineBuf.WriteString(fmt.Sprintf(`<img src="data:image/png;base64,%s" style="max-width:200px;"/>`, b64))
									}
									p.qrData = []byte{}
								}
							}
						}
						i = end; continue
					}
				} else {
					switch cmd {
					case 33: // GS ! n 字体大小
						if i+2 < len(raw) {
							n := raw[i+2]
							if n == 0x20 || n == 0x00 {
								p.widthMultiple = 1
								p.heightMultiple = 1
							} else {
								p.widthMultiple = int((n>>4)&0x0F) + 1
								p.heightMultiple = int(n&0x0F) + 1
							}
							i += 3; continue
						}
					case 86: // GS V 切纸
						p.flushLine()
						p.sb.WriteString(`<hr style="border-top: 1px dashed #000; margin: 10px 0;">`)
						if i+2 < len(raw) {
							m := raw[i+2]
							if (m == 0 || m == 1 || m == 48 || m == 49) && i+3 < len(raw) {
								i += 4; continue
							}
							i += 3; continue
						}
						i += 2; continue
					case 72, 81, 102, 104, 119, 66: // GS H n 等带1参数
						if i+2 < len(raw) { i += 3; continue }
					case 76, 87: // GS L nL nH 带2参数
						if i+3 < len(raw) { i += 4; continue }
					case 50: // GS 2
						i += 2; continue
					}
					i += 2; continue
				}
			}
			i++; continue
		} else if b == 28 { // FS
			p.flushText()
			if i+1 < len(raw) {
				cmd := raw[i+1]
				switch cmd {
				case 33: // FS ! n 汉字字体模式
					if i+2 < len(raw) {
						n := raw[i+2]
						if (n & 16) != 0 { p.bold = true } else if n == 0 { p.bold = false }
						if (n & 32) != 0 { p.underline = true } else if n == 0 { p.underline = false }
						if n == 0 { p.bold = false }
						
						p.heightMultiple = 1
						if (n & 8) != 0 { p.heightMultiple = 2 }
						p.widthMultiple = 1
						if (n & 1) != 0 || (n & 4) != 0 { p.widthMultiple = 2 }
						i += 3; continue
					}
				case 38, 46: // FS & , FS .
					i += 2; continue
				case 83: // FS S n1 n2
					if i+3 < len(raw) { i += 4; continue }
				case 67, 73, 74, 99, 110, 45: // FS C n 等
					if i+2 < len(raw) { i += 3; continue }
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
			p.textBuf = append(p.textBuf, b)
			i++; continue
		}
	}
	p.flushLine()
	return p.sb.String()
}
