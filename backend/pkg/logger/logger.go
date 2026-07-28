package logger

import (
	"fmt"
	"log"
	"os"
	"sync"
)

var (
	infoLogger  *log.Logger
	errorLogger *log.Logger
	initOnce    sync.Once
)

func Init() {
	initOnce.Do(func() {
		infoLogger = log.New(os.Stdout, "[INFO] ", log.Ldate|log.Ltime|log.Lshortfile)
		errorLogger = log.New(os.Stderr, "[ERROR] ", log.Ldate|log.Ltime|log.Lshortfile)
	})
}

func Info(format string, v ...interface{}) {
	Init()
	infoLogger.Output(2, fmt.Sprintf(format, v...))
}

func Error(format string, v ...interface{}) {
	Init()
	errorLogger.Output(2, fmt.Sprintf(format, v...))
}

func Fatal(format string, v ...interface{}) {
	Init()
	errorLogger.Output(2, fmt.Sprintf(format, v...))
	os.Exit(1)
}
