// Command loadgen sends realistic Sentry SDK events to a running Burrow and
// reports throughput, latency, and the server's CPU and memory (read from
// /proc, so Linux only). It produced the numbers in docs/benchmarks.md.
//
//	go run ./tools/loadgen -url http://127.0.0.1:8000 -project 1 -key <public key> \
//	    -pid $(pgrep -x burrow) -dur 30s -conc 8 -issues 200 [-rate 50]
package main

import (
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

func eventJSON(i, issues int) []byte {
	id := make([]byte, 16)
	rand.Read(id)
	n := i % issues
	// A realistic Python SDK event: 3 frames with source context and vars,
	// 12 breadcrumbs, tags, request, user, contexts. ~6 KB of JSON.
	var crumbs []string
	for c := 0; c < 12; c++ {
		crumbs = append(crumbs, fmt.Sprintf(`{"timestamp":%d,"category":"http","type":"http","message":"GET /api/items/%d","level":"info","data":{"status_code":200,"method":"GET","url":"https://api.example.com/items/%d"}}`, time.Now().Unix()-int64(60-c), c, c))
	}
	frame := func(fn, mod string, line int) string {
		return fmt.Sprintf(`{"function":"%s","module":"%s","filename":"%s.py","abs_path":"/srv/app/%s.py","lineno":%d,"in_app":true,
"pre_context":["def %s(request):","    subtotal = basket.subtotal()","    count = basket.count()"],
"context_line":"    total = subtotal / count","post_context":["    return render(request, 'summary.html', {'total': total})",""],
"vars":{"count":"0","subtotal":"129.00","request":"<WSGIRequest: POST '/checkout'>","basket":"<Basket id=%d items=0>"}}`, fn, mod, mod, mod, line, fn, i)
	}
	return []byte(fmt.Sprintf(`{"event_id":"%s","timestamp":%d,"platform":"python","level":"error","logger":"django.request",
"server_name":"web-%02d","release":"shop@2.%d.0","environment":"production","transaction":"/checkout/%d",
"exception":{"values":[{"type":"ZeroDivisionError%d","value":"division by zero (attempt %d)","mechanism":{"type":"django","handled":false},
"stacktrace":{"frames":[%s,%s,%s]}}]},
"breadcrumbs":{"values":[%s]},
"tags":{"browser":"Chrome 131","os":"Linux","region":"eu-west-1","customer_tier":"pro"},
"user":{"id":"%d","email":"user%d@example.com","ip_address":"203.0.113.7"},
"request":{"method":"POST","url":"https://shop.example.com/checkout","headers":{"User-Agent":"Mozilla/5.0","Accept":"text/html"},"data":{"coupon":"SPRING"}},
"contexts":{"runtime":{"name":"CPython","version":"3.12.4"},"os":{"name":"Linux"}},
"sdk":{"name":"sentry.python","version":"2.19.2"}}`,
		hex.EncodeToString(id), time.Now().Unix(), i%8, n%5, n, n, i,
		frame("dispatch_request", "flask.app", 1497), frame("handle", fmt.Sprintf("shop.views%d", n), 30), frame("checkout", fmt.Sprintf("shop.cart%d", n), 42),
		strings.Join(crumbs, ","), i, i))
}

func envelope(ev []byte) []byte {
	var b bytes.Buffer
	b.WriteString("{}\n{\"type\":\"event\"}\n")
	if err := json.Compact(&b, ev); err != nil {
		panic(err)
	}
	b.WriteString("\n")
	var gz bytes.Buffer
	w := gzip.NewWriter(&gz)
	w.Write(b.Bytes())
	w.Close()
	return gz.Bytes()
}

// procStats returns RSS, peak RSS (KiB) and CPU ticks (utime+stime) for pid.
func procStats(pid int) (rss, hwm int, ticks int64) {
	st, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return 0, 0, 0
	}
	for _, l := range strings.Split(string(st), "\n") {
		f := strings.Fields(l)
		if len(f) >= 2 && f[0] == "VmRSS:" {
			rss, _ = strconv.Atoi(f[1])
		}
		if len(f) >= 2 && f[0] == "VmHWM:" {
			hwm, _ = strconv.Atoi(f[1])
		}
	}
	s, _ := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	str := string(s)
	f := strings.Fields(str[strings.LastIndex(str, ")")+2:])
	u, _ := strconv.ParseInt(f[11], 10, 64)
	k, _ := strconv.ParseInt(f[12], 10, 64)
	return rss, hwm, u + k
}

func main() {
	base := flag.String("url", "", "")
	project := flag.String("project", "1", "")
	key := flag.String("key", "", "")
	pid := flag.Int("pid", 0, "")
	dur := flag.Duration("dur", 20*time.Second, "")
	conc := flag.Int("conc", 8, "")
	issues := flag.Int("issues", 200, "distinct issues")
	rate := flag.Int("rate", 0, "target events/s across all workers (0 = as fast as possible)")
	flag.Parse()

	url := fmt.Sprintf("%s/api/%s/envelope/", *base, *project)
	auth := "Sentry sentry_key=" + *key + ", sentry_version=7"
	var compact bytes.Buffer
	json.Compact(&compact, eventJSON(0, *issues))
	sample := compact.Bytes()
	fmt.Printf("event JSON %d bytes, gzipped envelope %d bytes\n", len(sample), len(envelope(sample)))

	_, _, t0 := procStats(*pid)
	start := time.Now()
	var sent, failed atomic.Int64
	var mu sync.Mutex
	var lat []time.Duration
	var maxRSS int
	done := make(chan struct{})
	go func() {
		for {
			select {
			case <-done:
				return
			case <-time.After(200 * time.Millisecond):
				r, _, _ := procStats(*pid)
				if r > maxRSS {
					maxRSS = r
				}
			}
		}
	}()
	var wg sync.WaitGroup
	var counter atomic.Int64
	client := &http.Client{Transport: &http.Transport{MaxIdleConnsPerHost: *conc}}
	var tick <-chan time.Time
	if *rate > 0 {
		tick = time.NewTicker(time.Second / time.Duration(*rate)).C
	}
	for w := 0; w < *conc; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Since(start) < *dur {
				if tick != nil {
					<-tick
				}
				i := int(counter.Add(1))
				body := envelope(eventJSON(i, *issues))
				req, _ := http.NewRequest("POST", url, bytes.NewReader(body))
				req.Header.Set("Content-Encoding", "gzip")
				req.Header.Set("X-Sentry-Auth", auth)
				t := time.Now()
				resp, err := client.Do(req)
				d := time.Since(t)
				if err != nil || resp.StatusCode != 200 {
					failed.Add(1)
					if resp != nil {
						resp.Body.Close()
					}
					continue
				}
				resp.Body.Close()
				sent.Add(1)
				mu.Lock()
				lat = append(lat, d)
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(done)
	el := time.Since(start).Seconds()
	_, hwm, t1 := procStats(*pid)
	sort.Slice(lat, func(a, b int) bool { return lat[a] < lat[b] })
	pct := func(p float64) time.Duration {
		if len(lat) == 0 {
			return 0
		}
		return lat[int(float64(len(lat)-1)*p)]
	}
	cpuSec := float64(t1-t0) / 100
	fmt.Printf("sent %d ok, %d failed in %.1fs = %.0f events/s\n", sent.Load(), failed.Load(), el, float64(sent.Load())/el)
	fmt.Printf("latency p50 %v p95 %v p99 %v\n", pct(.5).Round(time.Microsecond*10), pct(.95).Round(time.Microsecond*10), pct(.99).Round(time.Microsecond*10))
	fmt.Printf("server CPU %.1fs (%.2f cores avg), %.2f ms CPU per event\n", cpuSec, cpuSec/el, cpuSec*1000/float64(sent.Load()))
	fmt.Printf("server RSS max sampled %.1f MiB, peak (VmHWM) %.1f MiB\n", float64(maxRSS)/1024, float64(hwm)/1024)
}
