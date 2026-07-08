	package main
	import (
		"encoding/json"
		"flag"
		"fmt"
		"io"
		"log"
		"net"
		"os"
		"path/filepath"
		"time"
	)
	// Config 定义配置文件结构
	type Config struct {
		LatestFile string `json:"latest_file"`
		BackupDir  string `json:"backup_dir"`
		ListenPort int    `json:"listen_port"`
	}
	// 全局配置实例
	var appConfig = Config{
		LatestFile: "latest.prn", // 默认值
		BackupDir:  "./backups",  // 默认值
		ListenPort: 9100,         // 默认端口
	}
	const bufferSize = 64 * 1024 // 64KB 缓冲区
	func main() {
		// 1. 解析命令行参数
		// 定义 -c 参数，默认值为 "config.json"，如果用户不传 -c，则尝试读取同目录下的 config.json
		configPath := flag.String("c", "config.json", "指定配置文件路径 (例: -c /etc/posprinter/config.json)")
		flag.Parse()
		// 2. 加载配置文件
		loadConfig(*configPath)
		// 3. 初始化备份文件夹
		if err := os.MkdirAll(appConfig.BackupDir, 0755); err != nil {
			log.Fatalf("无法创建备份目录 '%s': %v", appConfig.BackupDir, err)
		}
		// 4. 检查主文件所在目录是否存在
		latestFileDir := filepath.Dir(appConfig.LatestFile)
		if latestFileDir != "." && latestFileDir != "" {
			if err := os.MkdirAll(latestFileDir, 0755); err != nil {
				log.Fatalf("无法创建主文件目录 '%s': %v", latestFileDir, err)
			}
		}
		// 5. 启动 TCP 服务监听
		listener, err := net.Listen("tcp", fmt.Sprintf(":%d", appConfig.ListenPort))
		if err != nil {
			log.Fatalf("监听端口 %d 失败: %v", appConfig.ListenPort, err)
		}
		defer listener.Close()
		log.Printf("ESC/POS RAW 接收服务已启动，监听端口: %d", appConfig.ListenPort)
		log.Printf("使用的配置文件: %s", *configPath)
		log.Printf("主文件路径: %s", appConfig.LatestFile)
		log.Printf("备份目录路径: %s", appConfig.BackupDir)
		for {
			conn, err := listener.Accept()
			if err != nil {
				log.Printf("接受连接失败: %v", err)
				continue
			}
			go handleConnection(conn)
		}
	}
	// loadConfig 读取并解析指定路径的配置文件，失败则使用默认配置
	func loadConfig(configPath string) {
		data, err := os.ReadFile(configPath)
		if err != nil {
			if os.IsNotExist(err) {
				log.Printf("未找到配置文件 '%s'，将使用默认配置和当前运行路径。\n", configPath)
			} else {
				log.Printf("警告: 读取配置文件 '%s' 失败: %v，将使用默认配置。\n", configPath, err)
			}
			return
		}
		// 解析 JSON 到全局配置变量
		if err := json.Unmarshal(data, &appConfig); err != nil {
			log.Printf("警告: 解析配置文件 '%s' 失败: %v，将使用默认配置。\n", configPath, err)
			return
		}
		// 如果配置文件中某项为空，回退到默认值
		if appConfig.LatestFile == "" {
			appConfig.LatestFile = "latest.prn"
		}
		if appConfig.BackupDir == "" {
			appConfig.BackupDir = "./backups"
		}
		if appConfig.ListenPort == 0 {
			appConfig.ListenPort = 9100
		}
	}
	func handleConnection(conn net.Conn) {
		defer conn.Close()
		clientAddr := conn.RemoteAddr().String()
		log.Printf("收到来自 %s 的打印数据连接", clientAddr)
		// 1. 准备覆盖写入的主文件
		latestFile, err := os.Create(appConfig.LatestFile)
		if err != nil {
			log.Printf("创建主文件失败 (%s): %v", appConfig.LatestFile, err)
			return
		}
		defer latestFile.Close()
		// 2. 准备备份文件（以时间戳命名）
		timestamp := time.Now().Format("20060102_150405")
		backupFileName := fmt.Sprintf("%s.raw", timestamp)
		backupFilePath := filepath.Join(appConfig.BackupDir, backupFileName)
		backupFile, err := os.Create(backupFilePath)
		if err != nil {
			log.Printf("创建备份文件失败 (%s): %v", backupFilePath, err)
			return
		}
		defer backupFile.Close()
		// 3. 同时接收数据并写入两个目标
		writer := io.MultiWriter(latestFile, backupFile)
		bytesCopied, err := io.CopyBuffer(writer, conn, make([]byte, bufferSize))
		if err != nil {
			log.Printf("数据传输异常 (来自 %s): %v", clientAddr, err)
			return
		}
		log.Printf("来自 %s 的数据接收完成，共 %d 字节。已覆盖至 %s 并备份至 %s",
			clientAddr, bytesCopied, appConfig.LatestFile, backupFilePath)
	}
