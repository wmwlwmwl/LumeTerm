package updatedownload

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// 真实网络测速：直连/代理 × 单线程/多线程。
// 默认跳过；显式运行：
//
//	LUMIN_UPDATE_SPEED_TEST=1 go test -count=1 -v -run TestUpdateDownloadSpeedLive -timeout 20m ./internal/updatedownload
func TestUpdateDownloadSpeedLive(t *testing.T) {
	if os.Getenv("LUMIN_UPDATE_SPEED_TEST") != "1" {
		t.Skip("set LUMIN_UPDATE_SPEED_TEST=1 to run live download speed test")
	}

	const (
		directURL    = "https://github.com/wmwlwmwl/LumeTerm/releases/download/v1.2.2.1/LumeTerm-1.2.2.1-windows-amd64-installer.exe"
		expectedSize = int64(18832855)
	)
	proxies := []struct {
		name string
		url  string
	}{
		{"ghproxy.net", "https://ghproxy.net/" + directURL},
		{"gh-proxy.com", "https://gh-proxy.com/" + directURL},
		{"proxy.gitwarp.top", "https://proxy.gitwarp.top/" + directURL},
	}

	dir := t.TempDir()
	client := NewHTTPClient()
	client.Timeout = 3 * time.Minute
	downloader := Downloader{Client: client}

	type result struct {
		name string
		mbps float64
		sec  float64
		err  string
		size int64
	}
	var results []result

	runOne := func(name, rawURL, mode string) {
		safe := strings.Map(func(r rune) rune {
			switch r {
			case '/', '\\', ':', '*', '?', '"', '<', '>', '|':
				return '_'
			default:
				return r
			}
		}, name)
		target := filepath.Join(dir, safe+".bin")
		_ = os.Remove(target)
		start := time.Now()
		var err error
		switch mode {
		case "multi":
			err = downloader.downloadMultiPart(client, nil, rawURL, target)
		case "single":
			err = downloader.downloadSingleThread(client, nil, rawURL, target)
		default:
			t.Fatalf("unknown mode %s", mode)
		}
		elapsed := time.Since(start).Seconds()
		if err != nil {
			results = append(results, result{name: name, err: err.Error(), sec: elapsed})
			t.Logf("%-36s FAIL  %.1fs  %v", name, elapsed, err)
			_ = os.Remove(target)
			return
		}
		stat, statErr := os.Stat(target)
		size := int64(0)
		if statErr == nil {
			size = stat.Size()
		}
		_ = os.Remove(target)
		if size <= 0 {
			results = append(results, result{name: name, err: "empty file", sec: elapsed})
			t.Logf("%-36s FAIL  empty file after %.1fs", name, elapsed)
			return
		}
		mbps := float64(size) / 1024 / 1024 / elapsed
		results = append(results, result{name: name, mbps: mbps, sec: elapsed, size: size})
		t.Logf("%-36s OK    %6.2f MB/s  %5.1fs  %d bytes", name, mbps, elapsed, size)
		if expectedSize > 0 && size != expectedSize {
			t.Logf("  note: size mismatch want %d got %d", expectedSize, size)
		}
	}

	runOne("direct-multi", directURL, "multi")
	runOne("direct-single", directURL, "single")
	for _, proxy := range proxies {
		runOne("proxy-single/"+proxy.name, proxy.url, "single")
		runOne("proxy-multi/"+proxy.name, proxy.url, "multi")
	}

	t.Log("---- summary ----")
	best := -1.0
	bestName := ""
	for _, item := range results {
		if item.err != "" {
			t.Logf("%-36s %s (%.1fs)", item.name, item.err, item.sec)
			continue
		}
		t.Logf("%-36s %6.2f MB/s  %.1fs", item.name, item.mbps, item.sec)
		if item.mbps > best {
			best = item.mbps
			bestName = item.name
		}
	}
	if bestName != "" {
		t.Logf("fastest: %s (%.2f MB/s)", bestName, best)
	}
}
