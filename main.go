package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"

	"GithubProxy/config"

	"github.com/gin-contrib/gzip"
	"github.com/gin-gonic/gin"
	"github.com/imroc/req/v3"
)

var (
	exps = []*regexp.Regexp{
		regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/([^/]+)/(?:releases|archive)/.*`),
		regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/([^/]+)/(?:blob|raw)/.*`),
		regexp.MustCompile(`^(?:https?://)?github\.com/([^/]+)/([^/]+)/(?:info|git-).*`),
		regexp.MustCompile(`^(?:https?://)?raw\.github(?:usercontent|)\.com/([^/]+)/([^/]+)/.+?/.+`),
		regexp.MustCompile(`^(?:https?://)?gist\.github\.com/([^/]+)/.+?/.+`),
	}
)

var (
	router *gin.Engine
	cfg    *config.Config
)

func init() {
	var err error
	cfg, err = config.LoadConfig("/data/ghproxy/config/config.yaml")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	fmt.Printf("Loaded config: %v\n", cfg)

	gin.SetMode(gin.ReleaseMode)
	router = gin.Default()

	router.Use(gzip.Gzip(gzip.DefaultCompression))

	router.GET("/", func(c *gin.Context) {
		c.Redirect(http.StatusMovedPermanently, "https://ghproxy0rtt.1888866.xyz/")
	})

	router.GET("/api/healthcheck", func(c *gin.Context) {
		c.String(http.StatusOK, "OK")
	})

	router.NoRoute(noRouteHandler(cfg))
}

func main() {
	logFile, err := os.OpenFile(cfg.LogFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Fatalf("Failed to open log file: %v", err)
	}
	defer logFile.Close()
	log.SetOutput(logFile)
	log.Println("Log Initialization Complete")

	if err := router.Run(fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)); err != nil {
		log.Fatalf("Error starting server: %v", err)
	}
}

func noRouteHandler(config *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		rawPath := strings.TrimPrefix(c.Request.URL.RequestURI(), "/")
		if matches := checkURL(rawPath); matches != nil {
			rawPath = "https://" + matches[2]
			if exps[1].MatchString(rawPath) {
				rawPath = strings.Replace(rawPath, "/blob/", "/raw/", 1)
			}
			log.Printf("Request: %s %s", c.Request.Method, rawPath)
			proxyRequest(c, rawPath, config)
		} else {
			c.String(http.StatusForbidden, "Invalid input.")
		}
	}
}

func proxyRequest(c *gin.Context, rawPath string, config *config.Config) {
	for i, exp := range exps {
		if exp.MatchString(rawPath) {
			switch i {
			case 0, 1, 3, 4:
				log.Printf("%s Matched EXPS[%d] - USE proxy-chrome", rawPath, i)
				proxyChrome(c, rawPath, config)
			case 2:
				log.Printf("%s Matched EXPS[2] - USE proxy-git", rawPath)
				proxyGit(c, rawPath, config)
			}
			return
		}
	}
	c.String(http.StatusForbidden, "Invalid input.")
}

func proxyGit(c *gin.Context, u string, config *config.Config) {
	proxyWithClient(c, u, config, "git/2.33.1")
}

func proxyChrome(c *gin.Context, u string, config *config.Config) {
	proxyWithClient(c, u, config, "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/128.0.0.0 Safari/537.36")
}

func proxyWithClient(c *gin.Context, u string, config *config.Config, userAgent string) {
	client := req.C().SetUserAgent(userAgent).SetTLSFingerprintChrome()
	method := c.Request.Method
	log.Printf("%s Method: %s", u, method)

	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		logAndRespond(c, http.StatusInternalServerError, "Failed to read request body", err)
		return
	}
	defer c.Request.Body.Close()

	req := client.R().SetBody(body)
	copyHeaders(req, c.Request.Header)

	resp, err := sendRequest(req, method, u)
	if err != nil {
		logAndRespond(c, http.StatusInternalServerError, "Failed to send request", err)
		return
	}
	defer resp.Body.Close()

	if err := handleResponseSize(resp, config, c); err != nil {
		return
	}

	copyHeadersToContext(c, resp.Header)
	c.Status(resp.StatusCode)
	if _, err := io.Copy(c.Writer, resp.Body); err != nil {
		log.Printf("Failed to copy response body: %v", err)
	}
}

func sendRequest(req *req.Request, method, url string) (*req.Response, error) {
	switch method {
	case "GET":
		return req.Get(url)
	case "POST":
		return req.Post(url)
	case "PUT":
		return req.Put(url)
	case "DELETE":
		return req.Delete(url)
	default:
		return nil, fmt.Errorf("unsupported method: %s", method)
	}
}

func handleResponseSize(resp *req.Response, config *config.Config, c *gin.Context) error {
	contentLength := resp.Header.Get("Content-Length")
	if contentLength != "" {
		if size, err := strconv.Atoi(contentLength); err == nil && size > config.SizeLimit {
			finalURL := resp.Request.URL.String()
			c.Redirect(http.StatusMovedPermanently, finalURL)
			log.Printf("Redirecting to %s due to size limit (%d bytes)", finalURL, size)
			return fmt.Errorf("response size exceeds limit")
		}
	}
	return nil
}

func checkURL(u string) []string {
	for _, exp := range exps {
		if matches := exp.FindStringSubmatch(u); matches != nil {
			log.Printf("URL matched: %s, Matches: %v", u, matches[1:])
			return matches[1:]
		}
	}
	log.Printf("Invalid URL: %s", u)
	return nil
}

func copyHeaders(req *req.Request, headers http.Header) {
	for key, values := range headers {
		for _, value := range values {
			req.SetHeader(key, value)
		}
	}
}

func copyHeadersToContext(c *gin.Context, headers http.Header) {
	for key, values := range headers {
		for _, value := range values {
			c.Header(key, value)
		}
	}
}

func logAndRespond(c *gin.Context, status int, message string, err error) {
	log.Printf("%s: %v", message, err)
	c.String(status, fmt.Sprintf("server error: %v", err))
}
