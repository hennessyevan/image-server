package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io/ioutil"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/dgrijalva/jwt-go"

	webp "github.com/chai2010/webp"
	"github.com/disintegration/imaging"

	imageserver_source_http "github.com/hennessyevan/image-server/server"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3/s3manager"
	"github.com/disintegration/gift"
	"github.com/golang/groupcache"
	"github.com/pierrre/imageserver"
	imageserver_cache "github.com/pierrre/imageserver/cache"
	imageserver_cache_file "github.com/pierrre/imageserver/cache/file"
	imageserver_cache_groupcache "github.com/pierrre/imageserver/cache/groupcache"
	imageserver_cache_memory "github.com/pierrre/imageserver/cache/memory"
	imageserver_http "github.com/pierrre/imageserver/http"
	imageserver_http_gift "github.com/pierrre/imageserver/http/gift"
	imageserver_http_image "github.com/pierrre/imageserver/http/image"
	imageserver_image "github.com/pierrre/imageserver/image"
	_ "github.com/pierrre/imageserver/image/gif"
	imageserver_image_gift "github.com/pierrre/imageserver/image/gift"
	_ "github.com/pierrre/imageserver/image/jpeg"
	_ "github.com/pierrre/imageserver/image/png"

	imageserver_http_cors "github.com/hennessyevan/image-server/cors"
)

const (
	groupcacheName = "imageserver"
	urlPrefix      = "https://s3.amazonaws.com/parishconnect-bkt/"
)

var (
	flagHTTP            = "5000"
	flagMaxUploadSize   = int64(6 * (1 << 25))
	flagUploadPath      = "/tmp"
	flagCache           = int64(128 * (1 << 20))
	flagGroupcache      = int64(128 * (1 << 20))
	flagGroupcachePeers string
	flagFile            = ".cache"
)

func main() {
	parseFlags()
	startHTTPServer()
}

func parseFlags() {
	flag.StringVar(&flagHTTP, "http", flagHTTP, "HTTP")
	flag.StringVar(&flagUploadPath, "uploadPath", flagUploadPath, "UploadPath")
	flag.Int64Var(&flagMaxUploadSize, "maxUploadSize", flagMaxUploadSize, "MaxUploadSize")
	flag.Int64Var(&flagGroupcache, "groupcache", flagGroupcache, "Groupcache")
	flag.StringVar(&flagGroupcachePeers, "groupcache-peers", flagGroupcachePeers, "Groupcache peers")
	flag.StringVar(&flagFile, "file", flagFile, "File")
	flag.Parse()
}

func startHTTPServer() {
	http.Handle("/", http.StripPrefix("/", newImageHTTPHandler()))
	http.Handle("/upload", uploadFileHandler())
	http.Handle("/favicon.ico", http.NotFoundHandler())
	initGroupcacheHTTPPool() // it automatically registers itself to "/_groupcache"
	http.HandleFunc("/stats", groupcacheStatsHTTPHandler)

	port := os.Getenv("PORT")
	if port == "" {
		port = flagHTTP
	}

	fmt.Printf("Listening on port %s\n\n", port)
	err := http.ListenAndServe(":"+port, nil)
	if err != nil {
		panic(err)
	}
}

func CreateDirIfNotExist(dir string) {
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		err = os.MkdirAll(dir, 0755)
		if err != nil {
			panic(err)
		}
	}
}

func newImageHTTPHandler() http.Handler {
	var handler http.Handler = &imageserver_http.Handler{
		Parser: imageserver_http.ListParser([]imageserver_http.Parser{
			&imageserver_http.SourcePathParser{},
			&imageserver_http_gift.ResizeParser{},
			&imageserver_http_image.FormatParser{},
			&imageserver_http_image.QualityParser{},
		}),
		Server: newServer(),
	}

	handler = &imageserver_http.ExpiresHandler{
		Handler: handler,
		Expires: 7 * 24 * time.Hour,
	}

	handler = &imageserver_http.CacheControlPublicHandler{
		Handler: handler,
	}

	handler = &imageserver_http_cors.CorsHandler{
		Handler: handler,
	}

	return handler
}

func renderError(w http.ResponseWriter, message string, statusCode int) {
	w.WriteHeader(http.StatusBadRequest)
	w.Write([]byte(message))
}

func randToken(len int) string {
	b := make([]byte, len)
	rand.Read(b)
	return fmt.Sprintf("%x", b)
}

func verifyToken(tokenString string) bool {
	os.Setenv("APP_SECRET", "my_secret_key")
	secret := []byte(os.Getenv("APP_SECRET"))
	token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
		return secret, nil
	})

	if err != nil {
		return false
	}

	if token.Valid {
		return true
	}
	return false

}

func uploadFileHandler() http.HandlerFunc {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token, err := r.Cookie("PC_CDN_TOKEN")
		if err != nil {
			renderError(w, "NO TOKEN", http.StatusForbidden)
			return
		}

		if ok := verifyToken(token.Value); !ok {
			renderError(w, "FORBIDDEN", http.StatusForbidden)
			return
		}

		if r.Method != "POST" {
			renderError(w, "INVALID REQUEST", http.StatusMethodNotAllowed)
			return
		}

		r.Body = http.MaxBytesReader(w, r.Body, flagMaxUploadSize)
		if err := r.ParseMultipartForm(flagMaxUploadSize); err != nil {
			renderError(w, "FILE TOO BIG", http.StatusBadRequest)
			return
		}

		fileType := r.PostFormValue("type")
		file, _, err := r.FormFile("uploadFile")
		parish := ""
		parish = r.PostFormValue("parish")
		if err != nil {
			renderError(w, "INVALID FILE", http.StatusBadRequest)
			return
		}

		defer file.Close()
		fileBytes, err := ioutil.ReadAll(file)
		if err != nil {
			renderError(w, "INVALID FILE", http.StatusBadRequest)
			return
		}

		filetype := http.DetectContentType(fileBytes)
		fmt.Println(filetype)
		if filetype != "image/jpeg" && filetype != "image/jpg" &&
			filetype != "image/gif" && filetype != "image/png" &&
			filetype != "image/webp" {
			renderError(w, "INVALID FILE TYPE", http.StatusBadRequest)
			return
		}

		var imgSrc image.Image
		if filetype == "image/png" {
			imgSrc, _ = png.Decode(bytes.NewReader(fileBytes))
		} else if filetype == "image/gif" {
			imgSrc, _ = gif.Decode(bytes.NewReader(fileBytes))
		} else if filetype == "image/webp" {
			imgSrc, _ = webp.Decode(bytes.NewReader(fileBytes))
		} else {
			imgSrc, _ = jpeg.Decode(bytes.NewReader(fileBytes))
		}

		s := fmt.Sprintf("file size: %d", len(fileBytes))
		fmt.Println(s)
		var newImage = imaging.Resize(imgSrc, 0, 800, imaging.MitchellNetravali)

		fileName := randToken(12)
		// fileEndings, err := mime.ExtensionsByType(filetype)
		if err != nil {
			renderError(w, "CANT READ FILE TYPE", http.StatusInternalServerError)
			return
		}
		fullFileName := parish + "/" + fileName + ".jpg"
		_, currentFile, _, _ := runtime.Caller(0)
		path := filepath.Join(filepath.Dir(currentFile), flagUploadPath)
		newPath := filepath.Join(path, fullFileName)
		fmt.Printf("File Type: %s, File: %s\n", fileType, newPath)

		// Maybe creates temp local directory for parish and keeps it for future use
		CreateDirIfNotExist("tmp/" + parish)

		newFile, err := os.Create(newPath)
		if err != nil {
			renderError(w, "CANT WRITE FILE", http.StatusInternalServerError)
			return
		}

		defer newFile.Close()
		err = imaging.Encode(newFile, newImage, imaging.JPEG, imaging.JPEGQuality(70))

		if err != nil {
			renderError(w, "CANT WRITE FILE", http.StatusInternalServerError)
			return
		}

		sess, err := session.NewSession(&aws.Config{
			Region: aws.String("us-east-1"),
		})
		if err != nil {
			renderError(w, "CANT CONNECT TO AWS SESSION", http.StatusInternalServerError)
			return
		}

		file, err = os.Open(newPath)
		if err != nil {
			renderError(w, "UNABLE TO OPEN FILE", http.StatusInternalServerError)
			return
		}

		defer file.Close()

		uploader := s3manager.NewUploader(sess)
		_, err = uploader.Upload(&s3manager.UploadInput{
			Bucket:      aws.String("parishconnect-bkt"),
			Key:         aws.String(fullFileName),
			Body:        file,
			ContentType: aws.String(filetype),
		})
		if err != nil {
			renderError(w, "CANT UPLOAD FILE", http.StatusInternalServerError)
			os.Remove(newPath)
			return
		}
		os.Remove(newPath)
		w.Write([]byte(fullFileName))
	})
}

func newServer() imageserver.Server {
	srv := newServerImage()
	srv = newServerLimit(srv)
	srv = newServerFile(srv)
	return srv
}

func newServerImage() imageserver.Server {
	return &imageserver.HandlerServer{
		Server: &imageserver_source_http.Server{},
		Handler: &imageserver_image.Handler{
			Processor: &imageserver_image_gift.ResizeProcessor{
				DefaultResampling: gift.LanczosResampling,
			},
		},
	}
}

func newServerGroupcache(srv imageserver.Server) imageserver.Server {
	if flagGroupcache <= 0 {
		return srv
	}
	return imageserver_cache_groupcache.NewServer(
		srv,
		imageserver_cache.NewParamsHashKeyGenerator(sha256.New),
		groupcacheName,
		flagGroupcache,
	)
}

func newServerLimit(srv imageserver.Server) imageserver.Server {
	return imageserver.NewLimitServer(srv, runtime.GOMAXPROCS(0)*2)
}

func initGroupcacheHTTPPool() {
	self := (&url.URL{Scheme: "http", Host: flagHTTP}).String()
	var peers []string
	peers = append(peers, self)
	for _, p := range strings.Split(flagGroupcachePeers, ",") {
		if p == "" {
			continue
		}
		peer := (&url.URL{Scheme: "http", Host: p}).String()
		peers = append(peers, peer)
	}
	pool := groupcache.NewHTTPPool(self)
	pool.Context = imageserver_cache_groupcache.HTTPPoolContext
	pool.Transport = imageserver_cache_groupcache.NewHTTPPoolTransport(nil)
	pool.Set(peers...)
}

func groupcacheStatsHTTPHandler(w http.ResponseWriter, req *http.Request) {
	gp := groupcache.GetGroup(groupcacheName)
	if gp == nil {
		http.Error(w, fmt.Sprintf("group %s not found", groupcacheName), http.StatusServiceUnavailable)
		return
	}
	type cachesStats struct {
		Main groupcache.CacheStats
		Hot  groupcache.CacheStats
	}
	type stats struct {
		Group  groupcache.Stats
		Caches cachesStats
	}
	data, err := json.MarshalIndent(
		stats{
			Group: gp.Stats,
			Caches: cachesStats{
				Main: gp.CacheStats(groupcache.MainCache),
				Hot:  gp.CacheStats(groupcache.HotCache),
			},
		},
		"",
		"	",
	)
	if err != nil {
		http.Error(w, http.StatusText(http.StatusInternalServerError), http.StatusInternalServerError)
		return
	}
	_, _ = w.Write(data)
}

func newServerCacheMemory(srv imageserver.Server) imageserver.Server {
	if flagCache <= 0 {
		return srv
	}
	return &imageserver_cache.Server{
		Server:       srv,
		Cache:        imageserver_cache_memory.New(flagCache),
		KeyGenerator: imageserver_cache.NewParamsHashKeyGenerator(sha256.New),
	}
}

func newServerFile(srv imageserver.Server) imageserver.Server {
	if flagFile == "" {
		return srv
	}

	CreateDirIfNotExist(flagFile)

	cch := imageserver_cache_file.Cache{Path: flagFile}
	kg := imageserver_cache.NewParamsHashKeyGenerator(sha256.New)
	return &imageserver_cache.Server{
		Server:       srv,
		Cache:        &cch,
		KeyGenerator: kg,
	}
}
