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
		"sync"
		"time"
		"unicode/utf8"
		"github.com/skip2/go-qrcode"
		"golang.org/x/text/encoding/simplifiedchinese"
	)
	// Config 定义配置文件结构
	type Config struct {
		LatestFile    string `json:"latest_file"`
		BackupDir     string `json:"backup_dir"`
		ListenPort    int    `json:"listen_port"`
		WebPort       int    `json:"web_port"`
		PrinterDevice string `json:"printer_device"`
		Verbose       bool   `json:"verbose"` // 新增：控制日志输出
	}
	var appConfig = Config{
		LatestFile:    "latest.prn",
		BackupDir:     "./backups",
		ListenPort:    9100,
		WebPort:       8080,
		PrinterDevice: "/dev/usb/lp0",
		Verbose:       false,
	}
	var printMutex sync.Mutex
	// 封装日志函数，根据配置决定是否输出
	func logInfo(format string, v ...interface{}) {
		if appConfig.Verbose {
			log.Printf(format, v...)
		}
	}
	func logFatal(format string, v ...interface{}) {
		if appConfig.Verbose {
			log.Fatalf(format, v...)
		}
		os.Exit(1)
	}
	func main() {
		os.Setenv("LANG", "C.UTF-8")
		os.Setenv("LC_ALL", "C.UTF-8")
		configPath := flag.String("c", "config.json", "Config file path")
		flag.Parse()
		loadConfig(*configPath)
		if err := os.MkdirAll(appConfig.BackupDir, 0755); err != nil {
			logFatal("Failed to create backup dir: %v", err)
		}
		latestFileDir := filepath.Dir(appConfig.LatestFile)
		if latestFileDir != "." && latestFileDir != "" {
			if err := os.MkdirAll(latestFileDir, 0755); err != nil {
				logFatal("Failed to create latest file dir: %v", err)
			}
		}
		go startWebServer()
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", appConfig.ListenPort))
		if err != nil {
			logFatal("Failed to listen on port %d: %v", appConfig.ListenPort, err)
		}
		defer listener.Close()
		logInfo("ESC/POS RAW Server started. Listening on TCP port: %d", appConfig.ListenPort)
		logInfo("Web Preview started: http://localhost:%d", appConfig.WebPort)
		logInfo("Local Printer Device: %s", appConfig.PrinterDevice)
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
		http.HandleFunc("/print", printHandler)
		logFatal("Web server failed: %v", http.ListenAndServe(fmt.Sprintf(":%d", appConfig.WebPort), nil))
	}
	func indexHandler(w http.ResponseWriter, r *http.Request) {
		htmlContent := `
	<!DOCTYPE html>
	<html lang="zh-CN">
	<head>
	    <meta charset="UTF-8">
	    <title>ESC/POS 实时小票预览</title>
	    <style>
	        html, body { margin: 0; padding: 0; background-color: #f0f0f0; overflow-x: hidden; }
	        body { font-family: sans-serif; text-align: center; }
	        .status { color: #888; padding: 1vh 0; font-size: 2vh; position: fixed; top: 0; width: 100%; background: #f0f0f0; z-index: 10; }
	        .status button { margin-left: 15px; padding: 5px 10px; cursor: pointer; }
	        #receipt-placeholder { padding-top: 6vh; }
	        #receipt-wrapper {
	            transform-origin: top center;
	            display: inline-block;
	        }
	        .esc-receipt {
	          font-family: 'Courier New', monospace;
	          font-size: 12px;
	          width: 32ch;
	          padding: 10px 1ch;
	          box-sizing: border-box;
	          text-align: left;
	          display: block;
	          background: #fff;
	          box-shadow: 0 0 15px rgba(0,0,0,0.1);
	        }
	    </style>
	</head>
	<body>
	    <div class="status" id="status">
	        等待打印数据...
	        <button onclick="triggerPrint()">立即打印本机小票</button>
	    </div>
	    <div id="receipt-placeholder">
	        <div id="receipt-wrapper">
	            <div id="receipt" class="esc-receipt"></div>
	        </div>
	    </div>
	    <script>
	        function adjustScale() {
	            const receipt = document.getElementById('receipt');
	            const wrapper = document.getElementById('receipt-wrapper');
	            if (!receipt || receipt.offsetWidth === 0) return;
	            const actualWidth = receipt.offsetWidth;
	            const windowWidth = window.innerWidth;
	            const scale = (windowWidth * 0.9) / actualWidth;
	            wrapper.style.transform = "scale(" + scale + ")";
	            const actualHeight = receipt.offsetHeight;
	            const placeholder = document.getElementById('receipt-placeholder');
	            placeholder.style.height = (actualHeight * scale) + "px";
	        }
	        async function fetchReceipt() {
	            try {
	                const response = await fetch('/data?ts=' + new Date().getTime());
	                if (response.ok) {
	                    const html = await response.text();
	                    document.getElementById('receipt').innerHTML = html;
	                    document.getElementById('status').childNodes[0].nodeValue = "最后刷新: " + new Date().toLocaleTimeString() + " ";
	                    adjustScale();
	                }
	            } catch (e) {}
	        }
	        async function triggerPrint() {
	            try {
	                const response = await fetch('/print');
	                const msg = await response.text();
	                alert(msg);
	            } catch (e) {
	                alert("打印请求失败");
	            }
	        }
	        window.addEventListener('resize', adjustScale);
	        setInterval(fetchReceipt, 1000);
	        fetchReceipt();
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
	func printHandler(w http.ResponseWriter, r *http.Request) {
		printMutex.Lock()
		defer printMutex.Unlock()
		rawData, err := os.ReadFile(appConfig.LatestFile)
		if err != nil {
			http.Error(w, "Read data failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		printerFile, err := os.OpenFile(appConfig.PrinterDevice, os.O_WRONLY, 0)
		if err != nil {
			http.Error(w, "Open printer failed: "+err.Error(), http.StatusInternalServerError)
			return
		}
		defer printerFile.Close()
		chunkSize := 1024
		for i := 0; i < len(rawData); i += chunkSize {
			end := i + chunkSize
			if end > len(rawData) {
				end = len(rawData)
			}
			_, err = printerFile.Write(rawData[i:end])
			if err != nil {
				http.Error(w, "Write to printer failed: "+err.Error(), http.StatusInternalServerError)
				return
			}
			time.Sleep(50 * time.Millisecond)
		}
		w.Write([]byte("Print job sent to local USB printer"))
	}
	func loadConfig(configPath string) {
		data, err := os.ReadFile(configPath)
		if err != nil {
			logInfo("Config file '%s' not found. Using defaults.", configPath)
			return
		}
		if err := json.Unmarshal(data, &appConfig); err != nil {
			logInfo("Failed to parse config: %v. Using defaults.", err)
			return
		}
		if appConfig.LatestFile == "" { appConfig.LatestFile = "latest.prn" }
		if appConfig.BackupDir == "" { appConfig.BackupDir = "./backups" }
		if appConfig.ListenPort == 0 { appConfig.ListenPort = 9100 }
		if appConfig.WebPort == 0 { appConfig.WebPort = 8080 }
		if appConfig.PrinterDevice == "" { appConfig.PrinterDevice = "/dev/usb/lp0" }
	}
	// === TCP 接收处理 ===
	func handleConnection(conn net.Conn) {
		defer conn.Close()
		clientAddr := conn.RemoteAddr().String()
		conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		defer conn.SetReadDeadline(time.Time{})
		logInfo("Connection from %s, listening...", clientAddr)
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
					} else if b2 == 0x70 { // ESC p m t1 t2 (钱箱开启脉冲)
						reader.Read(make([]byte, 3))
						continue
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
		isValidPrint := false
		if totalBytes >= 10 && bytes.Contains(dataBuf.Bytes(), []byte{0x0A}) {
			isValidPrint = true
		} else if totalBytes >= 50 {
			isValidPrint = true
		}
		if !isValidPrint {
			logInfo("Dropped probe/invalid data from %s (%d bytes)", clientAddr, totalBytes)
			return
		}
		logInfo("Received %d bytes from %s, writing to file...", totalBytes, clientAddr)
		latestFile, err := os.Create(appConfig.LatestFile)
		if err != nil { logInfo("Failed to create latest file: %v", err); return }
		defer latestFile.Close()
		timestamp := time.Now().Format("20060102_150405")
		backupFile, err := os.Create(filepath.Join(appConfig.BackupDir, fmt.Sprintf("%s.raw", timestamp)))
		if err != nil { logInfo("Failed to create backup file: %v", err); return }
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
		maxHeightMultiple int
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
				if p.heightMultiple > p.maxHeightMultiple {
					p.maxHeightMultiple = p.heightMultiple
				}
				style += "display: inline-block; vertical-align: middle; line-height: 1;"
				if p.widthMultiple == p.heightMultiple {
					style += fmt.Sprintf("font-size: %dem;", p.heightMultiple)
				} else {
					if p.heightMultiple > 1 {
						style += fmt.Sprintf("transform: scaleY(%d);", p.heightMultiple)
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
		lineContent := strings.TrimRight(p.lineBuf.String(), "&nbsp;")
		divStyle := fmt.Sprintf("white-space: nowrap; text-align:%s; line-height: %fem;", align, 1.2 * float64(p.maxHeightMultiple))
		if len(lineContent) == 0 {
			p.sb.WriteString(fmt.Sprintf(`<div style="%s">&nbsp;</div>`, divStyle))
		} else {
			p.sb.WriteString(fmt.Sprintf(`<div style="%s">%s</div>`, divStyle, lineContent))
		}
		p.lineBuf.Reset()
		p.maxHeightMultiple = 1
	}
	func escToHTML(raw []byte) string {
		p := &parserState{
			widthMultiple:     1,
			heightMultiple:    1,
			maxHeightMultiple: 1,
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
					case 112: // ESC p m t1 t2
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
