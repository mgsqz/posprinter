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
 
// Config 露篓脪氓脜盲脰脙脦脛录镁陆谩鹿鹿
type Config struct {
	LatestFile    string `json:"latest_file"`
	BackupDir     string `json:"backup_dir"`
	ListenPort    int    `json:"listen_port"`
	WebPort       int    `json:"web_port"`
	PrinterDevice string `json:"printer_device"` // 脨脗脭枚拢潞卤戮碌脴麓貌脫隆禄煤脡猫卤赂脗路戮露
}
 
var appConfig = Config{
	LatestFile:    "latest.prn",
	BackupDir:     "./backups",
	ListenPort:    9100,
	WebPort:       8080,
	PrinterDevice: "/dev/usb/lp0",
}
 
var printMutex sync.Mutex // 路脌脰鹿虏垄路垄麓貌脫隆鲁氓脥禄
 
func main() {
	configPath := flag.String("c", "config.json", "脰赂露篓脜盲脰脙脦脛录镁脗路戮露")
	flag.Parse()
 
	loadConfig(*configPath)
 
	if err := os.MkdirAll(appConfig.BackupDir, 0755); err != nil {
		log.Fatalf("脦脼路篓麓麓陆篓卤赂路脻脛驴脗录: %v", err)
	}
 
	latestFileDir := filepath.Dir(appConfig.LatestFile)
	if latestFileDir != "." && latestFileDir != "" {
		if err := os.MkdirAll(latestFileDir, 0755); err != nil {
			log.Fatalf("脦脼路篓麓麓陆篓脰梅脦脛录镁脛驴脗录: %v", err)
		}
	}
 
	go startWebServer()
 
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", appConfig.ListenPort))
	if err != nil {
		log.Fatalf("录脿脤媒露脣驴脷 %d 脢搂掳脺: %v", appConfig.ListenPort, err)
	}
	defer listener.Close()
 
	log.Printf("ESC/POS RAW 陆脫脢脮路镁脦帽脪脩脝么露炉拢卢录脿脤媒露脣驴脷: %d", appConfig.ListenPort)
	log.Printf("Web 脭陇脌脌路镁脦帽脪脩脝么露炉: http://localhost:%d", appConfig.WebPort)
	log.Printf("卤戮碌脴麓貌脫隆禄煤脡猫卤赂: %s", appConfig.PrinterDevice)
 
	for {
		conn, err := listener.Accept()
		if err != nil {
			continue
		}
		go handleConnection(conn)
	}
}
 
// === Web 路镁脦帽 ===
func startWebServer() {
	http.HandleFunc("/", indexHandler)
	http.HandleFunc("/data", dataHandler)
	http.HandleFunc("/print", printHandler) // 脨脗脭枚拢潞Webhook 麓貌脫隆脗路脫脡
	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%d", appConfig.WebPort), nil))
}
 
func indexHandler(w http.ResponseWriter, r *http.Request) {
	htmlContent := `
<!DOCTYPE html>
<html lang="zh-CN">
<head>
    <meta charset="UTF-8">
    <title>ESC/POS 脢碌脢卤脨隆脝卤脭陇脌脌</title>
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
        碌脠麓媒麓貌脫隆脢媒戮脻...
        <button onclick="triggerPrint()">脕垄录麓麓貌脫隆卤戮禄煤脨隆脝卤</button>
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
                    document.getElementById('status').childNodes[0].nodeValue = "脳卯潞贸脣垄脨脗: " + new Date().toLocaleTimeString() + " ";
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
                alert("麓貌脫隆脟毛脟贸脢搂掳脺");
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
		w.Write([]byte(`<div style="color:#999;">脭脻脦脼麓貌脫隆脢媒戮脻</div>`))
		return
	}
	htmlStr := escToHTML(rawData)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write([]byte(htmlStr))
}
 
// 脨脗脭枚拢潞麓娄脌铆 Webhook 麓貌脫隆脟毛脟贸
func printHandler(w http.ResponseWriter, r *http.Request) {
	printMutex.Lock()
	defer printMutex.Unlock()
 
	rawData, err := os.ReadFile(appConfig.LatestFile)
	if err != nil {
		http.Error(w, "露脕脠隆麓貌脫隆脢媒戮脻脢搂掳脺: "+err.Error(), http.StatusInternalServerError)
		return
	}
 
	// 麓貌驴陋卤戮碌脴麓貌脫隆禄煤脡猫卤赂
	printerFile, err := os.OpenFile(appConfig.PrinterDevice, os.O_WRONLY, 0)
	if err != nil {
		http.Error(w, "麓貌驴陋麓貌脫隆禄煤脡猫卤赂脢搂掳脺: "+err.Error(), http.StatusInternalServerError)
		return
	}
	defer printerFile.Close()
 
	// 脨麓脠毛 RAW 脢媒戮脻
	_, err = printerFile.Write(rawData)
	if err != nil {
		http.Error(w, "脨麓脠毛麓貌脫隆禄煤脢搂掳脺: "+err.Error(), http.StatusInternalServerError)
		return
	}
 
	w.Write([]byte("麓貌脫隆脠脦脦帽脪脩路垄脣脥脰脕卤戮碌脴USB麓貌脫隆禄煤"))
}
 
func loadConfig(configPath string) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		log.Printf("脦麓脮脪碌陆脜盲脰脙脦脛录镁 '%s'拢卢陆芦脢鹿脫脙脛卢脠脧脜盲脰脙隆拢\n", configPath)
		return
	}
	if err := json.Unmarshal(data, &appConfig); err != nil {
		log.Printf("陆芒脦枚脜盲脰脙脦脛录镁脢搂掳脺: %v拢卢陆芦脢鹿脫脙脛卢脠脧脜盲脰脙隆拢\n", err)
		return
	}
	if appConfig.LatestFile == "" { appConfig.LatestFile = "latest.prn" }
	if appConfig.BackupDir == "" { appConfig.BackupDir = "./backups" }
	if appConfig.ListenPort == 0 { appConfig.ListenPort = 9100 }
	if appConfig.WebPort == 0 { appConfig.WebPort = 8080 }
	if appConfig.PrinterDevice == "" { appConfig.PrinterDevice = "/dev/usb/lp0" }
}
 
// === TCP 陆脫脢脮麓娄脌铆 ===
func handleConnection(conn net.Conn) {
	defer conn.Close()
	clientAddr := conn.RemoteAddr().String()
 
	conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	defer conn.SetReadDeadline(time.Time{})
 
	log.Printf("脢脮碌陆脌麓脳脭 %s 碌脛脕卢陆脫拢卢驴陋脢录录脿脤媒脕梅...", clientAddr)
 
	var dataBuf bytes.Buffer
	reader := bufio.NewReader(conn)
	totalBytes := 0
 
	for {
		b, err := reader.ReadByte()
		if err != nil {
			break
		}
 
		// === 脢碌脢卤脳麓脤卢脰赂脕卯脌鹿陆脴脫毛禄脴赂麓 ===
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
 
	// 脜脨露脧脢脟路帽脦陋脫脨脨搂碌脛麓貌脫隆脢媒戮脻
	isValidPrint := false
	if totalBytes >= 10 && bytes.Contains(dataBuf.Bytes(), []byte{0x0A}) {
		isValidPrint = true
	} else if totalBytes >= 50 {
		isValidPrint = true
	}
 
	if !isValidPrint {
		log.Printf("露陋脝煤脤陆虏芒/脦脼脨搂脢媒戮脻: 脌麓脳脭 %s (陆枚 %d 脳脰陆脷)", clientAddr, totalBytes)
		return
	}
 
	log.Printf("脌麓脳脭 %s 陆脫脢脮脥锚鲁脡拢卢鹿虏 %d 脳脰陆脷拢卢脮媒脭脷脨麓脠毛脦脛录镁...", clientAddr, totalBytes)
 
	latestFile, err := os.Create(appConfig.LatestFile)
	if err != nil { log.Printf("麓麓陆篓脰梅脦脛录镁脢搂掳脺: %v", err); return }
	defer latestFile.Close()
 
	timestamp := time.Now().Format("20060102_150405")
	backupFile, err := os.Create(filepath.Join(appConfig.BackupDir, fmt.Sprintf("%s.raw", timestamp)))
	if err != nil { log.Printf("麓麓陆篓卤赂路脻脦脛录镁脢搂掳脺: %v", err); return }
	defer backupFile.Close()
 
	latestFile.Write(dataBuf.Bytes())
	backupFile.Write(dataBuf.Bytes())
}
 
// === ESC/POS 脳麓脤卢陆芒脦枚脝梅 ===
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
				case 64: // ESC @ 鲁玫脢录禄炉
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
				case 112: // ESC p m t1 t2 (脟庐脧盲驴陋脝么脗枚鲁氓拢卢3赂枚虏脦脢媒)
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
				case 51, 74, 77, 82, 85, 86, 99, 103, 123, 32: // ESC 3 n 碌脠麓酶1虏脦脢媒
					if i+2 < len(raw) { i += 3; continue }
				}
				i += 2; continue
			}
			i++; continue
		} else if b == 29 { // GS
			p.flushText()
			if i+1 < len(raw) {
				cmd := raw[i+1]
				if cmd == 40 && i+4 < len(raw) && raw[i+2] == 107 { // GS ( k (QR脗毛)
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
					case 33: // GS ! n 脳脰脤氓麓贸脨隆
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
					case 86: // GS V 脟脨脰陆
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
					case 72, 81, 102, 104, 119, 66: // GS H n 碌脠麓酶1虏脦脢媒
						if i+2 < len(raw) { i += 3; continue }
					case 76, 87: // GS L nL nH 麓酶2虏脦脢媒
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
				case 33: // FS ! n 潞潞脳脰脳脰脤氓脛拢脢陆
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
				case 67, 73, 74, 99, 110, 45: // FS C n 碌脠
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
