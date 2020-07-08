// Copyright (c) 2019 Louis Tarango - me@lou.ist

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
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/denisbrodbeck/machineid"
	"github.com/spf13/viper"
)

// Track submits events to mixpanel, disregard errors
func Track(event string, data map[string]interface{}) {
	if distinctID, err := machineid.ProtectedID(Application); err == nil {
		if len(Analytics) > 0 && viper.GetBool("analytics") {
			params := map[string]interface{}{
				"event": event,
			}
			properties := map[string]interface{}{
				"token":       Analytics,
				"distinct_id": distinctID,
				"version":     Version,
				"time":        fmt.Sprintf("%v", time.Now().Unix()),
			}
			for key, value := range data {
				if _, ok := data[key]; ok {
					properties[key] = value
				}
			}
			params["properties"] = properties
			if data, err := json.Marshal(params); err == nil {
				if req, err := http.NewRequest("GET", fmt.Sprintf(mixpanel, "track"), nil); err == nil {
					q := req.URL.Query()
					q.Add("data", base64.StdEncoding.EncodeToString(data))
					req.URL.RawQuery = q.Encode()
					(&http.Client{}).Do(req)
				}
			}
		}
	}
}

// MixUser manages a user on mixpanel, disregard errors
func MixUser(data map[string]interface{}) {
	if distinctID, err := machineid.ProtectedID(Application); err == nil {
		if len(Analytics) > 0 && viper.GetBool("analytics") {
			params := map[string]interface{}{
				"$token":       Analytics,
				"$distinct_id": distinctID,
				"$time":        fmt.Sprintf("%v", time.Now().Unix()),
			}
			properties := map[string]interface{}{}
			for key, value := range data {
				if _, ok := data[key]; ok {
					properties[key] = value
				}
			}
			params["$set"] = properties
			if data, err := json.Marshal(params); err == nil {
				if req, err := http.NewRequest("GET", fmt.Sprintf(mixpanel, "engage"), nil); err == nil {
					q := req.URL.Query()
					q.Add("data", base64.StdEncoding.EncodeToString(data))
					req.URL.RawQuery = q.Encode()
					(&http.Client{}).Do(req)
				}
			}
		}
	}
}
