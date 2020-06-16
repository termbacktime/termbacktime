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

var (
	mixpanel = "https://api.mixpanel.com/%s/?ip=1"
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
