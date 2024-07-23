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

	"gopkg.in/yaml.v2"
)

type Config struct {
	Jsdelivr302         bool `yaml:"JSDelivr302"`
	Jsdelivr            bool `yaml:"JSDelivr"`
	MaxResponseBodySize int  `yaml:"maxResponseBodySize"`
}

var config Config

const (
	AssetURL      = "https://github.com/"
	Prefix        = "/"
	DefaultScheme = "https"
)

var whiteList = []string{} // 白名单，路径里面有包含字符的才会通过

type responseWriterWithLimit struct {
	http.ResponseWriter
	size         int
	mutex        sync.Mutex
	limitReached bool
}

// LoadConfig 从 YAML 配置文件加载配置
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

func (w *responseWriterWithLimit) Write(data []byte) (int, error) {
	w.mutex.Lock()
	defer w.mutex.Unlock()

	if w.limitReached {
		return 0, nil
	}

	newSize := w.size + len(data)
	if newSize > config.MaxResponseBodySize {
		// 超出限制，返回413状态码并关闭连接
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
	log.Printf("Config loaded: %v", config)
	http.HandleFunc("/", handleRequest)
	log.Fatal(http.ListenAndServe(":8080", nil))
}

func handleRequest(w http.ResponseWriter, r *http.Request) {

	wrappedWriter := &responseWriterWithLimit{ResponseWriter: w}

	path := r.URL.Query().Get("q")
	if path != "" {
		redirectURL := fmt.Sprintf("%s://%s%s%s", DefaultScheme, r.Host, Prefix, path)
		http.Redirect(wrappedWriter, r, redirectURL, http.StatusMovedPermanently)
		return
	}

	path = r.URL.Path[len(Prefix):]
	if checkURL(path) {
		httpHandler(wrappedWriter, r, path)
	} else {
		proxyURL := AssetURL + path
		proxyRequest(wrappedWriter, r, proxyURL)
	}

	// 在处理完请求后，检查响应体是否超出限制，如果超出，则清空当前的响应体以释放内存
	if wrappedWriter.limitReached {
		// 清空当前的响应体
		wrappedWriter.ResponseWriter = &emptyResponseWriter{}
		log.Printf("Response body size limit reached for %s", r.URL.Path)
		log.Printf("Request %s %s %s %s", r.Method, r.Host, r.URL.Path, r.Proto)
		log.Printf("Clear response body for %s", r.URL.Path)
	}

	// 关闭请求体
	r.Body.Close()
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

func httpHandler(w http.ResponseWriter, r *http.Request, path string) {
	GithubPatterns := []*regexp.Regexp{
		regexp.MustCompile(`^github\.com/[^/]+/[^/]+/(releases|archive)/.*$`),
		regexp.MustCompile(`^github\.com/[^/]+/[^/]+/(blob|raw)/.*$`),
		regexp.MustCompile(`^github\.com/[^/]+/[^/]+/(info|git-).*$`),
		regexp.MustCompile(`^raw\.githubusercontent\.com/.*$`),
		regexp.MustCompile(`^objects\.githubusercontent\.com/.*$`),
	}

	GithubCDNPatterns := []*regexp.Regexp{
		regexp.MustCompile(`^raw\.githubusercontent\.com/.*$`),
		regexp.MustCompile(`^raw\.github\.com/.*$`),
	}

	for _, pattern := range GithubCDNPatterns {
		if matches := pattern.FindStringSubmatch(path); matches != nil {
			log.Printf("Path %s matched pattern %s", path, pattern.String())

			// 使用路径分割替代
			parts := strings.Split(path, "/")
			if len(parts) < 4 {
				http.NotFound(w, r)
				return
			}

			owner := parts[1]
			repo := parts[2]
			branch := parts[3]
			file := strings.Join(parts[4:], "/")

			// 构造新的 URL

			if config.Jsdelivr302 == true {
				newURL := "https://cdn.jsdelivr.net/gh/" + owner + "/" + repo + "@" + branch + "/" + file
				log.Printf("newURL: %s", newURL)
				http.Redirect(w, r, newURL, http.StatusMovedPermanently)
			} else if config.Jsdelivr302 == false && config.Jsdelivr == true {
				newURL := "cdn.jsdelivr.net/gh/" + owner + "/" + repo + "@" + branch + "/" + file
				log.Printf("newURL: %s", newURL)
				proxyRequest(w, r, newURL)
			} else {
				log.Printf("Path %s matched pattern %s", path, pattern.String())
				proxyRequest(w, r, path)
			}
			return
		}
	}

	matched := false
	for _, pattern := range GithubPatterns {
		if pattern.MatchString(path) {
			matched = true
			proxyRequest(w, r, path)
			log.Printf("Path %s matched pattern %s", path, pattern.String())
		}
	}

	if !matched {
		// 读取本地 HTML 文件
		htmlFilePath := "/data/caddy/pages/errors/404.html"
		htmlContent, err := ioutil.ReadFile(htmlFilePath)
		if err != nil {
			log.Printf("Error reading 404 HTML file: %v", err)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}

		// 返回自定义的 HTML 页面
		w.WriteHeader(http.StatusNotFound)
		w.Write(htmlContent)
		log.Printf("Path %s not found", path)
	}
}

func proxyRequest(w http.ResponseWriter, r *http.Request, urlStr string) {
	proxyURL, err := url.Parse(urlStr)
	if err != nil {
		log.Printf("Error parsing proxy URL: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	if proxyURL.Scheme == "" {
		proxyURL.Scheme = DefaultScheme
	}

	client := &http.Client{}
	req, err := http.NewRequest(r.Method, proxyURL.String(), r.Body)
	if err != nil {
		log.Printf("Creating request to %s failed: %v", proxyURL.String(), err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	req.Header = r.Header

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("Sending request to %s failed: %v", proxyURL.String(), err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
	defer resp.Body.Close()

	// 调用 ModifyResponse 函数修改响应头
	err = ModifyResponse(resp)
	if err != nil {
		log.Printf("Error modifying response: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}

	// 将响应头信息复制给客户端
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// 复制状态码
	w.WriteHeader(resp.StatusCode)

	// 分块读取响应体并拷贝到输出流
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		log.Printf("Copying response body failed: %v", err)
		http.Error(w, "Internal Server Error", http.StatusInternalServerError)
		return
	}
}

func ModifyResponse(resp *http.Response) error {
	resp.Header.Set("access-control-allow-origin", "*")
	resp.Header.Set("access-control-allow-methods", "GET,POST,PUT,PATCH,TRACE,DELETE,HEAD,OPTIONS")

	return nil
}

type emptyResponseWriter struct{}

func (e *emptyResponseWriter) Write(data []byte) (int, error) {
	return len(data), nil
}

func (e *emptyResponseWriter) WriteHeader(statusCode int) {}

func (e *emptyResponseWriter) Header() http.Header {
	return http.Header{}
}
