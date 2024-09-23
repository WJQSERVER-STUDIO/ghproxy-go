package main

import (
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"

	"github.com/gin-gonic/gin"
	"gopkg.in/yaml.v3"
)

type Config struct {
	Jsdelivr302         bool `yaml:"jsdelivr302"`
	Jsdelivr            bool `yaml:"jsdelivr"`
	MaxResponseBodySize int  `yaml:"maxResponseBodySize"`
}

var config Config

const (
	AssetURL      = "https://github.com/"
	Prefix        = "/"
	DefaultScheme = "https"
)

var whiteList = []string{}

type responseWriterWithLimit struct {
	gin.ResponseWriter
	size         int
	mutex        sync.Mutex
	limitReached bool
}

func LoadConfig(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	bytes, err := ioutil.ReadAll(file)
	if err != nil {
		return err
	}

	return yaml.Unmarshal(bytes, &config)
}

func api(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"MaxResponseBodySize": config.MaxResponseBodySize,
		"Jsdelivr302":         config.Jsdelivr302,
		"Jsdelivr":            config.Jsdelivr,
	})
}

func (w *responseWriterWithLimit) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.limitReached {
		return 0, nil
	}

	newSize := w.size + len(data)
	if newSize > config.MaxResponseBodySize {
		w.limitReached = true
		w.ResponseWriter.WriteHeader(http.StatusRequestEntityTooLarge)
		return 0, nil
	}

	w.size = newSize
	return w.ResponseWriter.Write(data)
}

func main() {
	logFile, err := os.OpenFile("/data/ghproxy/log/run.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		log.Printf("Log Initialization Failed: > %s", err)
	} else {
		defer logFile.Close()
		log.SetOutput(logFile)
		log.Println("Log Initialization Complete")
	}

	configErr := LoadConfig("/data/ghproxy/config/config.yaml")
	if configErr != nil {
		log.Fatalf("Error loading config: %v", configErr)
	}

	gin.SetMode(gin.ReleaseMode)
	log.Printf("Config loaded: %v", config)

	router := gin.Default()
	router.GET("/api", api)
	router.NoRoute(handleRequest)

	log.Fatal(router.Run(":8080"))
}

func handleRequest(c *gin.Context) {
	wrappedWriter := &responseWriterWithLimit{ResponseWriter: c.Writer}

	path := c.Query("q")
	if path != "" {
		redirectURL := fmt.Sprintf("%s://%s%s%s", DefaultScheme, c.Request.Host, Prefix, path)
		c.Redirect(http.StatusMovedPermanently, redirectURL)
		return
	}

	path = c.Request.URL.Path[len(Prefix):]
	if checkURL(path) {
		httpHandler(c, wrappedWriter, path)
	} else {
		proxyURL := AssetURL + path
		proxyRequest(c, wrappedWriter, proxyURL)
	}

	if wrappedWriter.limitReached {
		c.Status(http.StatusRequestEntityTooLarge)
		log.Printf("Response body size limit reached for %s", c.Request.URL.Path)
	}

	c.Request.Body.Close()
}

func checkURL(u string) bool {
	if len(whiteList) == 0 {
		return true
	}

	for _, pattern := range whiteList {
		if strings.Contains(u, pattern) {
			return true
		}
	}

	return false
}

func httpHandler(c *gin.Context, w *responseWriterWithLimit, path string) {
	GithubPatterns := []*regexp.Regexp{
		regexp.MustCompile(`^github\.com/[^/]+/[^/]+/(releases|archive)/.*`),
		regexp.MustCompile(`^github\.com/[^/]+/[^/]+/(blob|raw)/.*`),
		regexp.MustCompile(`^github\.com/[^/]+/[^/]+/(info|git-).*`),
		regexp.MustCompile(`^raw\.githubusercontent\.com/.*`),
		regexp.MustCompile(`^objects\.githubusercontent\.com/.*`),
	}

	GithubCDNPatterns := []*regexp.Regexp{
		regexp.MustCompile(`^raw\.githubusercontent\.com/.*`),
		regexp.MustCompile(`^raw\.github\.com/.*`),
	}

	for _, pattern := range GithubCDNPatterns {
		if matches := pattern.FindStringSubmatch(path); matches != nil {
			log.Printf("Path %s matched pattern %s", path, pattern.String())

			parts := strings.Split(path, "/")
			if len(parts) < 4 {
				c.String(http.StatusNotFound, "Not Found")
				return
			}

			owner := parts[1]
			repo := parts[2]
			branch := parts[3]
			file := strings.Join(parts[4:], "/")

			if config.Jsdelivr302 {
				newURL := "https://cdn.jsdelivr.net/gh/" + owner + "/" + repo + "@" + branch + "/" + file
				log.Printf("newURL: %s", newURL)
				c.Redirect(http.StatusMovedPermanently, newURL)
			} else if !config.Jsdelivr302 && config.Jsdelivr {
				newURL := "cdn.jsdelivr.net/gh/" + owner + "/" + repo + "@" + branch + "/" + file
				log.Printf("newURL: %s", newURL)
				proxyRequest(c, w, newURL)
			} else {
				log.Printf("Path %s matched pattern %s", path, pattern.String())
				proxyRequest(c, w, path)
			}
			return
		}
	}

	matched := false
	for _, pattern := range GithubPatterns {
		if pattern.MatchString(path) {
			matched = true
			proxyRequest(c, w, path)
			log.Printf("Path %s matched pattern %s", path, pattern.String())
		}
	}

	if !matched {
		htmlFilePath := "/data/caddy/pages/errors/404.html"
		htmlContent, err := ioutil.ReadFile(htmlFilePath)
		if err != nil {
			log.Printf("Error reading 404 HTML file: %v", err)
			c.String(http.StatusInternalServerError, "Internal Server Error")
			return
		}

		c.Data(http.StatusNotFound, "text/html", htmlContent)
		log.Printf("Path %s not found", path)
	}
}

func proxyRequest(c *gin.Context, w *responseWriterWithLimit, urlStr string) {
	proxyURL, err := url.Parse(urlStr)
	if err != nil {
		log.Printf("Error parsing proxy URL: %v", err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}

	if proxyURL.Scheme == "" {
		proxyURL.Scheme = DefaultScheme
	}

	client := &http.Client{}
	req, err := http.NewRequest(c.Request.Method, proxyURL.String(), c.Request.Body)
	if err != nil {
		log.Printf("Creating request to %s failed: %v", proxyURL.String(), err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}

	req.Header = c.Request.Header

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Sending request to %s failed: %v", proxyURL.String(), err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}
	defer resp.Body.Close()

	err = ModifyResponse(resp)
	if err != nil {
		log.Printf("Error modifying response: %v", err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}

	for key, values := range resp.Header {
		for _, value := range values {
			c.Writer.Header().Add(key, value)
		}
	}

	c.Writer.WriteHeader(resp.StatusCode)

	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("Copying response body failed: %v", err)
		c.String(http.StatusInternalServerError, "Internal Server Error")
		return
	}
}

func ModifyResponse(resp *http.Response) error {
	resp.Header.Set("access-control-allow-origin", "*")
	resp.Header.Set("access-control-allow-methods", "GET,POST,PUT,PATCH,TRACE,DELETE,HEAD,OPTIONS")

	return nil
}
