// tool/ip_info.go
package tool

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type IPInfoTool struct{}

func (i *IPInfoTool) Name() string { return "get_ip_info" }

func (i *IPInfoTool) Description() string {
	return "获取 IP 地址的地理位置信息，不提供 IP 则查询本机公网 IP。"
}

func (i *IPInfoTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"ip": map[string]interface{}{
				"type":        "string",
				"description": "IP 地址（可选）",
			},
		},
	}
}

func (i *IPInfoTool) Execute(args map[string]interface{}) (string, error) {
	ip, _ := args["ip"].(string)
	if ip == "" {
		// 获取本机公网 IP
		resp, err := http.Get("https://api.ipify.org?format=json")
		if err != nil {
			return fmt.Sprintf("获取公网 IP 失败: %v", err), nil
		}
		defer resp.Body.Close()
		var ipData struct {
			IP string `json:"ip"`
		}
		json.NewDecoder(resp.Body).Decode(&ipData)
		ip = ipData.IP
		if ip == "" {
			return "无法获取公网 IP", nil
		}
	}
	// 查询 IP 地理位置 (ip-api.com 免费)
	geoURL := fmt.Sprintf("http://ip-api.com/json/%s?fields=status,message,country,regionName,city,zip,lat,lon,isp,query", ip)
	resp, err := http.Get(geoURL)
	if err != nil {
		return fmt.Sprintf("查询地理位置失败: %v", err), nil
	}
	defer resp.Body.Close()
	var geo struct {
		Status  string  `json:"status"`
		Message string  `json:"message"`
		Country string  `json:"country"`
		Region  string  `json:"regionName"`
		City    string  `json:"city"`
		Zip     string  `json:"zip"`
		Lat     float64 `json:"lat"`
		Lon     float64 `json:"lon"`
		ISP     string  `json:"isp"`
		Query   string  `json:"query"`
	}
	json.NewDecoder(resp.Body).Decode(&geo)
	if geo.Status != "success" {
		return fmt.Sprintf("查询失败: %s", geo.Message), nil
	}
	return fmt.Sprintf("IP: %s\n国家: %s\n地区: %s\n城市: %s\n邮编: %s\n经纬度: %.4f, %.4f\nISP: %s",
		geo.Query, geo.Country, geo.Region, geo.City, geo.Zip, geo.Lat, geo.Lon, geo.ISP), nil
}
