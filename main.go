package main

import (
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"time"
)

const (
	listenPort   = 9100
	latestFile   = "latest.prn"     // 覆盖保存的RAW文件
	backupDir    = "./backups"      // 备份文件夹
	bufferSize   = 64 * 1024        // 64KB 缓冲区
)

func main() {
	// 初始化备份文件夹
	if err := os.MkdirAll(backupDir, 0755); err != nil {
		log.Fatalf("无法创建备份目录: %v", err)
	}

	// 启动 TCP 服务监听 9100 端口
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", listenPort))
	if err != nil {
		log.Fatalf("监听端口 %d 失败: %v", listenPort, err)
	}
	defer listener.Close()
	
	log.Printf("ESC/POS RAW 接收服务已启动，监听端口: %d", listenPort)

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("接受连接失败: %v", err)
			continue
		}
		// 使用协程处理每个连接，支持高并发
		go handleConnection(conn)
	}
}

func handleConnection(conn net.Conn) {
	defer conn.Close()
	
	// 获取客户端IP信息（可用于日后权限校验或日志）
	clientAddr := conn.RemoteAddr().String()
	log.Printf("收到来自 %s 的打印数据连接", clientAddr)

	// 1. 准备覆盖写入的主文件
	latestFilePath := filepath.Join(".", latestFile)
	latestFile, err := os.Create(latestFilePath)
	if err != nil {
		log.Printf("创建主文件失败: %v", err)
		return
	}
	defer latestFile.Close()

	// 2. 准备备份文件（以时间戳命名）
	timestamp := time.Now().Format("20060102_150405")
	backupFileName := fmt.Sprintf("%s.raw", timestamp)
	backupFilePath := filepath.Join(backupDir, backupFileName)
	backupFile, err := os.Create(backupFilePath)
	if err != nil {
		log.Printf("创建备份文件失败: %v", err)
		return
	}
	defer backupFile.Close()

	// 3. 同时接收数据并写入两个目标
	// 使用 MultiWriter 将网络流同时写入主文件和备份文件
	writer := io.MultiWriter(latestFile, backupFile)
	
	// 从连接中读取数据并写入
	bytesCopied, err := io.CopyBuffer(writer, conn, make([]byte, bufferSize))
	if err != nil {
		log.Printf("数据传输异常 (来自 %s): %v", clientAddr, err)
		return
	}

	log.Printf("来自 %s 的数据接收完成，共 %d 字节。已覆盖至 %s 并备份至 %s", 
		clientAddr, bytesCopied, latestFile.Name(), backupFilePath)
}
