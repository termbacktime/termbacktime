// Copyright (c) 2021 Louis Tarango - me@lou.ist

// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:

// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.

// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.

package cmd

import (
	"fmt"
	"io"
	"log"
	"os"
	"sync"
)

// Logger writes logs to a temp file to display on exit
type Logger struct {
	file *os.File
	*log.Logger
}

var (
	logger *Logger
	once   sync.Once
)

// Remove a log file
func (l *Logger) Remove() (bool, error) {
	f := l.file.Name()
	if _, err := os.Stat(f); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
	}
	if err := os.Remove(f); err != nil {
		return false, err
	}
	return true, nil
}

// Write wraps log.Println
func (l *Logger) Write(str string) *Logger {
	l.Println(str)
	return l
}

// Dump logger content
func (l *Logger) Dump() *Logger {
	if content, err := os.ReadFile(l.file.Name()); err == nil {
		fmt.Println(string(content))
	}
	return l
}

// Name returns the file name
func (l *Logger) Name() string {
	return l.file.Name()
}

// GetLogger returns a logger instance
func GetLogger(prefix ...string) *Logger {
	once.Do(func() {
		pre := "TermBackTime"
		if len(prefix) > 0 {
			pre = prefix[0]
		}
		file, err := os.CreateTemp("", fmt.Sprintf("%s-*.log", pre))
		if err == nil {
			logger = &Logger{
				file:   file,
				Logger: log.New(io.Writer(file), "", log.LstdFlags),
			}
		}
	})
	return logger
}
