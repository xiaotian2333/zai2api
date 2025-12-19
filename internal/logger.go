package internal

import (
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

var (
	currentLevel LogLevel = INFO
	levelNames            = map[LogLevel]string{
		DEBUG: "DEBUG",
		INFO:  "INFO",
		WARN:  "WARN",
		ERROR: "ERROR",
	}
	// ANSI 颜色
	levelColors = map[LogLevel]string{
		DEBUG: "\033[36m", // 青色
		INFO:  "\033[32m", // 绿色
		WARN:  "\033[33m", // 黄色
		ERROR: "\033[31m", // 红色
	}
	resetColor = "\033[0m"
)

// InitLogger 初始化日志（从配置读取日志级别）
func InitLogger() {
	// 从配置读取日志级别
	if Cfg != nil {
		if Cfg.DebugLogging {
			currentLevel = DEBUG
		}
	}

	// 环境变量可覆盖配置
	if level := getEnvString("LOG_LEVEL", ""); level != "" {
		switch strings.ToLower(level) {
		case "debug":
			currentLevel = DEBUG
		case "warn":
			currentLevel = WARN
		case "error":
			currentLevel = ERROR
		default:
			currentLevel = INFO
		}
	}
}

// getCallerFile 获取调用者的文件名
func getCallerFile(skip int) string {
	_, file, _, ok := runtime.Caller(skip)
	if !ok {
		return "unknown.go"
	}
	return filepath.Base(file)
}

func log(level LogLevel, skip int, format string, v ...interface{}) {
	if level < currentLevel {
		return
	}
	timestamp := time.Now().Format("2006/01/02 15:04:05")
	caller := getCallerFile(skip)
	msg := fmt.Sprintf(format, v...)
	// 格式: 时间 [模块.go] [等级] 信息
	fmt.Printf("%s [%s] %s[%s]%s %s\n", timestamp, caller, levelColors[level], levelNames[level], resetColor, msg)
}

func LogDebug(format string, v ...interface{}) {
	log(DEBUG, 2, format, v...)
}

func LogInfo(format string, v ...interface{}) {
	log(INFO, 2, format, v...)
}

func LogWarn(format string, v ...interface{}) {
	log(WARN, 2, format, v...)
}

func LogError(format string, v ...interface{}) {
	log(ERROR, 2, format, v...)
}
