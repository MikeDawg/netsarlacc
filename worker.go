package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/ioutil"
	"net"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// NewWorker creates, and returns a new Worker object. Its only argument
// is a channel that the worker can add itself to whenever it is done its
// work.
func NewWorker(id int, workerQueue chan chan WorkRequest) Worker {
	//Creating the worker
	worker := Worker{
		ID:          id,
		Work:        make(chan WorkRequest),
		WorkerQueue: workerQueue,
		QuitChan:    make(chan bool)}

	return worker
}

type Worker struct {
	ID          int
	Work        chan WorkRequest
	WorkerQueue chan chan WorkRequest
	QuitChan    chan bool
}

type Header struct {
	Method         string `json:"http_method"`
	User_Agent     string `json:"http_user_agent"`
	Content_Length string `json:"bytes_client"`
	Host           string `json:"url"`
	Referer        string `json:"http_referer"`
	Version        string `json:"http_version"`
}

type EncodedConn struct {
	Encode string `json:"raw_data"`
}

type LoggedRequest struct {
	Timestamp string `json:"timestamp"`
	Header
	Source      net.Addr `json:"src_ip"`  // Source IP net.Addr
	Destination string   `json:"dest_ip"` // Dest IP net.Addr
	EncodedConn
}

// This function "starts" the worker by starting a goroutine, that is
// an infinite "for-select" loop.
func (w *Worker) Start() {
	go func() {
		for {
			// Add ourselves into the worker queue.
			w.WorkerQueue <- w.Work
			select {
			case work := <-w.Work:
				// Receive a work request.
				buf := make([]byte, 4000)
				// Total request reads no more than 4kb set caps
				// Sets a read dead line. If it doesn't receive any information
				// Check to see if it'll accept trickled data
				// Whole transaction time no more than 500 mili
				//
				//work.Connection.SetReadBuffer()
				//60000
				work.Connection.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
				// If accpets trickled data, use timer below
				//timer := time.NewTimer(time.Millisecond * 500)
				sourceIP := work.Connection.RemoteAddr()
				bufSize, err := work.Connection.Read(buf)
				rawData := EncodedConn{Encode: hex.EncodeToString(buf[:bufSize])}
				if err != nil {
					fmt.Println("Error reading:", err.Error())
					AppLogger(err)
					work.Connection.Write([]byte("Error I/O timeout. \n"))
					work.Connection.Close()
				} else {
					validConnLogging, err := parseConn(buf, bufSize, rawData, sourceIP)
					if err != nil {
						fmt.Println(err)
						jsonLog, _ := ToJSON(rawData)
						ConnLogger(jsonLog)
						work.Connection.Close()
					} else {
						jsonLog, _ := ToJSON(validConnLogging)
						ConnLogger(jsonLog)
						absPath, _ := filepath.Abs("./netsarlacc/template/csirtResponse.html")
						data, err := ioutil.ReadFile(absPath)
						if err != nil {
							fmt.Println("error is ", err)
						}
						work.Connection.Write([]byte("HTTP 200 OK\r\nContent-Length: 10441\r\n\r\n"))
						work.Connection.Write(data)
						work.Connection.Close()
					}
				}

			case <-w.QuitChan:
				// We have been asked to stop.
				fmt.Printf("worker%d stopping\n", w.ID)
				return
			}
		}
	}()
}

// Stop tells the worker to stop listening for work requests.
// Note that the worker will only stop *after* it has finished its work.
func (w *Worker) Stop() {
	go func() {
		w.QuitChan <- true
	}()
}

func parseConn(buf []byte, bufSize int, raw EncodedConn, sourceIP net.Addr) (LoggedRequest, error) {
	s := string(buf[:])
	methodRegex, _ := regexp.Compile("^(GET |POST |HEAD |PUT |DELETE |TRACE |OPTIONS |CONNECT |PATCH )")
	protocolRegex, _ := regexp.Compile("HTTP\\/*.*")
	fieldsRegex, _ := regexp.Compile("^[A-z].*:(.*)$")
	requestLines := strings.Split(s, "\n")
	protocol := strings.Fields(requestLines[0])[2]
	var allHeaders map[string]string
	if methodRegex.MatchString(requestLines[0]) && protocolRegex.MatchString(protocol) {
		allHeaders = make(map[string]string)
		headerFields := string(buf[:strings.LastIndex(s, "\r\n")-2])
		scanner := bufio.NewScanner(strings.NewReader(headerFields))
		for scanner.Scan() {
			for scanner.Scan() {
				value := strings.SplitN(scanner.Text(), ":", 2)
				if !fieldsRegex.MatchString(scanner.Text()) {
					return LoggedRequest{}, errors.New("One or more of the header fields are invalid ")
				} else {
					allHeaders[value[0]] = strings.Join(value[1:len(value)], " ")
				}
			}
		}
		if err := scanner.Err(); err != nil {
			return LoggedRequest{}, err
		}
	} else {
		return LoggedRequest{}, errors.New("Error parsing headers or non http request")
	}
	header := Header{Method: strings.Fields(requestLines[0])[0], User_Agent: allHeaders["User-Agent"], Content_Length: allHeaders["Content-Length"], Host: "http://" + strings.Trim(allHeaders["Host"], " ") + strings.Fields(requestLines[0])[1], Referer: allHeaders["Referer"], Version: protocol}
	validConnLogging := LoggedRequest{Timestamp: time.Now().UTC().String(), Header: header, Source: sourceIP, Destination: allHeaders["Host"], EncodedConn: raw}
	return validConnLogging, nil
}

// ToJSON converts a struct to a JSON string
func ToJSON(data interface{}) (string, error) {
	b, err := json.Marshal(data)
	if err != nil {
		return "", err
	}
	return string(b), nil
}
