package mullvad

import "testing"

func TestParseSpeedtestJSONOokla(t *testing.T) {
	res := &SpeedtestResult{Raw: `{
  "type": "result",
  "ping": { "jitter": 0.4, "latency": 12.5 },
  "download": { "bandwidth": 12500000, "bytes": 150000000, "elapsed": 15000 },
  "upload": { "bandwidth": 2500000, "bytes": 30000000, "elapsed": 15000 }
}`}
	parseSpeedtestJSON(res)
	if !res.ParsedOK {
		t.Fatal("expected parsedOk")
	}
	if res.DownloadMbps != 100 {
		t.Fatalf("download Mbps=%v want 100", res.DownloadMbps)
	}
	if res.UploadMbps != 20 {
		t.Fatalf("upload Mbps=%v want 20", res.UploadMbps)
	}
	if res.LatencyMs != 12.5 {
		t.Fatalf("latency=%v want 12.5", res.LatencyMs)
	}
}

func TestParseSpeedtestJSONUnofficialBits(t *testing.T) {
	res := &SpeedtestResult{Raw: `{"download": 45000000, "upload": 12000000, "ping": 8.2}`}
	parseSpeedtestJSON(res)
	if !res.ParsedOK {
		t.Fatal("expected parsedOk")
	}
	if res.DownloadMbps != 45 {
		t.Fatalf("download Mbps=%v want 45", res.DownloadMbps)
	}
	if res.UploadMbps != 12 {
		t.Fatalf("upload Mbps=%v want 12", res.UploadMbps)
	}
	if res.LatencyMs != 8.2 {
		t.Fatalf("latency=%v want 8.2", res.LatencyMs)
	}
}

func TestParseSpeedtestSimpleHuman(t *testing.T) {
	res := &SpeedtestResult{Raw: `   Idle Latency:    11.20 ms
      Download:    94.21 Mbps (data used: 84.7 MB)
        Upload:    20.15 Mbps (data used: 36.4 MB)
`}
	parseSpeedtestSimple(res)
	if !res.ParsedOK {
		t.Fatal("expected parsedOk")
	}
	if res.DownloadMbps != 94.21 {
		t.Fatalf("download=%v", res.DownloadMbps)
	}
	if res.UploadMbps != 20.15 {
		t.Fatalf("upload=%v", res.UploadMbps)
	}
}
