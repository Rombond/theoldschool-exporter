package main

import (
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// -----------------------------------------------------------------------------
// Configuration
// -----------------------------------------------------------------------------

type config struct {
	Port           string
	MetricsPath    string
	ScrapeInterval time.Duration
	Username       string
	Password       string
	BaseURL        string
}

func loadConfig() config {
	baseURL := getEnvOrDefault("THEOLDSCHOOL_BASE_URL", "https://theoldschool.cc")
	return config{
		Port:           getEnvOrDefault("PORT", "9090"),
		MetricsPath:    getEnvOrDefault("METRICS_PATH", "/metrics"),
		Username:       os.Getenv("THEOLDSCHOOL_USERNAME"),
		Password:       os.Getenv("THEOLDSCHOOL_PASSWORD"),
		ScrapeInterval: parseDuration(os.Getenv("SCRAPE_INTERVAL"), 5*time.Minute),
		BaseURL:        strings.TrimSuffix(baseURL, "/"),
	}
}

func getEnvOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// -----------------------------------------------------------------------------
// Site client (UNIT3D: session login + HTML scraping)
// -----------------------------------------------------------------------------

const userAgent = "Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130.0.0.0 Safari/537.36"

var (
	formRe     = regexp.MustCompile(`(?is)<form[^>]*auth-form__form[^>]*>(.*?)</form>`)
	inputRe    = regexp.MustCompile(`(?is)<input\b[^>]*>`)
	attrRe     = regexp.MustCompile(`(?is)([a-z\-]+)\s*=\s*"([^"]*)"`)
	uploadRe   = regexp.MustCompile(`(?is)ratio-bar__uploaded[^>]*>(.*?)</li>`)
	downloadRe = regexp.MustCompile(`(?is)ratio-bar__downloaded[^>]*>(.*?)</li>`)
	tagRe      = regexp.MustCompile(`(?s)<[^>]*>`)
	sizeRe     = regexp.MustCompile(`([0-9][0-9\s.,]*)\s*([KMGTP]?i?B)`)
)

var errSessionExpired = fmt.Errorf("session expired")

// TheOldSchoolClient manages the session (cookie jar) with theoldschool.cc.
type TheOldSchoolClient struct {
	mu         sync.Mutex
	baseURL    string
	username   string
	password   string
	loggedIn   bool
	httpClient *http.Client
}

func newClient(baseURL, username, password string) *TheOldSchoolClient {
	jar, _ := cookiejar.New(nil)
	return &TheOldSchoolClient{
		baseURL:    baseURL,
		username:   username,
		password:   password,
		httpClient: &http.Client{Timeout: 20 * time.Second, Jar: jar},
	}
}

func (c *TheOldSchoolClient) IsAuthenticated() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.loggedIn
}

func (c *TheOldSchoolClient) get(path string) (string, string, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "text/html")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", resp.Request.URL.Path, fmt.Errorf("unexpected response: %s", resp.Status)
	}
	return string(body), resp.Request.URL.Path, nil
}

// Login loads the login form, copies every hidden field (CSRF _token,
// _captcha, honeypot timestamp), then posts the credentials.
func (c *TheOldSchoolClient) Login() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.loggedIn = false

	page, _, err := c.get("/login")
	if err != nil {
		return fmt.Errorf("failed to load login page: %w", err)
	}
	m := formRe.FindStringSubmatch(page)
	if m == nil {
		return fmt.Errorf("login form not found")
	}

	form := url.Values{}
	for _, in := range inputRe.FindAllString(m[1], -1) {
		attrs := map[string]string{}
		for _, a := range attrRe.FindAllStringSubmatch(in, -1) {
			attrs[strings.ToLower(a[1])] = html.UnescapeString(a[2])
		}
		if attrs["type"] == "hidden" && attrs["name"] != "" {
			form.Set(attrs["name"], attrs["value"])
		}
	}
	form.Set("_username", "") // honeypot, must stay empty
	form.Set("username", c.username)
	form.Set("password", c.password)
	form.Set("remember", "on")

	// UNIT3D rejects forms submitted too quickly (honeypot timestamp).
	time.Sleep(3 * time.Second)

	req, err := http.NewRequest(http.MethodPost, c.baseURL+"/login", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Referer", c.baseURL+"/login")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("login request failed: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.Request.URL.Path == "/login" || resp.StatusCode >= 400 {
		return fmt.Errorf("login failed (invalid credentials or rejected form): %s", resp.Status)
	}
	c.loggedIn = true
	fmt.Printf("[auth] Successfully authenticated as %s\n", c.username)
	return nil
}

// FetchMetrics reads the top-nav ratio bar of the home page.
// It re-logs in once if the session has expired.
func (c *TheOldSchoolClient) FetchMetrics() (*UserMetrics, error) {
	m, err := c.fetchOnce()
	if err == errSessionExpired {
		fmt.Println("[auth] Session expired, re-authenticating...")
		if lerr := c.Login(); lerr != nil {
			return nil, lerr
		}
		return c.fetchOnce()
	}
	return m, err
}

func (c *TheOldSchoolClient) fetchOnce() (*UserMetrics, error) {
	page, path, err := c.get("/")
	if err != nil {
		if path == "/login" {
			return nil, errSessionExpired
		}
		return nil, err
	}
	if path == "/login" || strings.Contains(page, "auth-form__form") {
		return nil, errSessionExpired
	}
	up, err := extractBytes(uploadRe, page)
	if err != nil {
		return nil, fmt.Errorf("upload: %w", err)
	}
	down, err := extractBytes(downloadRe, page)
	if err != nil {
		return nil, fmt.Errorf("download: %w", err)
	}
	return &UserMetrics{Uploaded: up, Downloaded: down}, nil
}

// extractBytes finds a block via re and converts its "511.96 GiB" text to bytes.
func extractBytes(re *regexp.Regexp, page string) (float64, error) {
	m := re.FindStringSubmatch(page)
	if m == nil {
		return 0, fmt.Errorf("ratio bar element not found")
	}
	return parseSize(html.UnescapeString(tagRe.ReplaceAllString(m[1], " ")))
}

// parseSize converts strings such as "511.96 GiB" (binary units) to bytes.
func parseSize(s string) (float64, error) {
	m := sizeRe.FindStringSubmatch(strings.ReplaceAll(s, "\u00a0", " "))
	if m == nil {
		return 0, fmt.Errorf("cannot parse size %q", strings.TrimSpace(s))
	}
	num := strings.ReplaceAll(strings.ReplaceAll(m[1], " ", ""), "\u00a0", "")
	if strings.Contains(num, ".") {
		num = strings.ReplaceAll(num, ",", "")
	} else {
		num = strings.ReplaceAll(num, ",", ".")
	}
	v, err := strconv.ParseFloat(num, 64)
	if err != nil {
		return 0, err
	}
	pow := map[string]int{"B": 0, "KiB": 1, "MiB": 2, "GiB": 3, "TiB": 4, "PiB": 5}[m[2]]
	for i := 0; i < pow; i++ {
		v *= 1024
	}
	return v, nil
}

// -----------------------------------------------------------------------------
// Domain types
// -----------------------------------------------------------------------------

type UserMetrics struct {
	Uploaded   float64
	Downloaded float64
}

// -----------------------------------------------------------------------------
// Prometheus metrics
// -----------------------------------------------------------------------------

type exporterMetrics struct {
	totalUploaded   prometheus.Gauge
	totalDownloaded prometheus.Gauge
}

func newExporterMetrics(reg prometheus.Registerer) *exporterMetrics {
	m := &exporterMetrics{
		totalUploaded: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "theoldschool",
			Name:      "total_uploaded_bytes",
			Help:      "Total uploaded bytes from theoldschool.cc",
		}),
		totalDownloaded: prometheus.NewGauge(prometheus.GaugeOpts{
			Namespace: "theoldschool",
			Name:      "total_downloaded_bytes",
			Help:      "Total downloaded bytes from theoldschool.cc",
		}),
	}
	reg.MustRegister(m.totalUploaded, m.totalDownloaded)
	return m
}

func (m *exporterMetrics) update(u *UserMetrics) {
	m.totalUploaded.Set(u.Uploaded)
	m.totalDownloaded.Set(u.Downloaded)
}

// -----------------------------------------------------------------------------
// HTTP handlers
// -----------------------------------------------------------------------------

type server struct {
	client  *TheOldSchoolClient
	metrics *exporterMetrics
}

func (s *server) metricsHandler(c *gin.Context) {
	if s.client.username != "" && s.client.password != "" {
		if !s.client.IsAuthenticated() {
			if err := s.client.Login(); err != nil {
				fmt.Printf("[auth] Login failed: %v\n", err)
			}
		}
	}
	if s.client.IsAuthenticated() {
		userMetrics, err := s.client.FetchMetrics()
		if err != nil {
			fmt.Printf("[metrics] Error scraping metrics: %v\n", err)
		} else {
			s.metrics.update(userMetrics)
		}
	}

	c.Header("Content-Type", "text/plain; version=0.0.4")
	promhttp.Handler().ServeHTTP(c.Writer, c.Request)
}

func (s *server) healthHandler(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":        "healthy",
		"authenticated": s.client.IsAuthenticated(),
	})
}

// -----------------------------------------------------------------------------
// Entrypoint
// -----------------------------------------------------------------------------

func main() {
	cfg := loadConfig()

	client := newClient(cfg.BaseURL, cfg.Username, cfg.Password)
	metrics := newExporterMetrics(prometheus.DefaultRegisterer)
	srv := &server{client: client, metrics: metrics}

	// Attempt auto-login on startup if credentials are provided.
	if cfg.Username != "" && cfg.Password != "" {
		fmt.Println("[auth] Credentials found, attempting auto-login...")
		if err := client.Login(); err != nil {
			fmt.Printf("[auth] Auto-login failed (non-fatal): %v\n", err)
		}
	} else {
		fmt.Println("[auth] No credentials provided, auto-login skipped")
	}

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	r.GET(cfg.MetricsPath, srv.metricsHandler)
	r.GET("/health", srv.healthHandler)

	addr := ":" + cfg.Port
	fmt.Printf("[server] Starting theoldschool exporter on %s\n", addr)
	fmt.Printf("[server] Metrics available at: http://localhost%s%s\n", addr, cfg.MetricsPath)
	fmt.Printf("[server] Scrape interval: %v\n", cfg.ScrapeInterval)

	if err := r.Run(addr); err != nil {
		fmt.Printf("[server] Error starting server: %v\n", err)
		os.Exit(1)
	}
}

// -----------------------------------------------------------------------------
// Helpers
// -----------------------------------------------------------------------------

// parseDuration parses a simple duration string (e.g. "30s", "5m", "2h").
// Returns fallback if the string is empty or cannot be parsed.
func parseDuration(s string, fallback time.Duration) time.Duration {
	s = strings.TrimSpace(s)
	if s == "" {
		return fallback
	}
	units := []struct {
		suffix string
		mult   time.Duration
	}{
		{"h", time.Hour},
		{"m", time.Minute},
		{"s", time.Second},
	}
	for _, u := range units {
		if strings.HasSuffix(s, u.suffix) {
			numStr := strings.TrimSuffix(s, u.suffix)
			var n int
			if _, err := fmt.Sscanf(numStr, "%d", &n); err == nil && n > 0 {
				return time.Duration(n) * u.mult
			}
		}
	}
	return fallback
}
