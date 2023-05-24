package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/aws/awserr"
	"github.com/aws/aws-sdk-go/aws/credentials"
	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"

	_ "github.com/maestroi/snapshot-service-api/docs"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
)

var config *Config
var sess *session.Session // global AWS session

type Config struct {
	FilePath   string `json:"file_path"`
	BucketName string `json:"bucket_name"`
	AccessKey  string `json:"access_key"`
	SecretKey  string `json:"secret_key"`
	Endpoint   string `json:"endpoint"`
	Region     string `json:"region"`
}

func init() {
	var configFilePath string
	flag.StringVar(&configFilePath, "config", "", "Path to the configuration file")
	flag.Parse()

	var err error
	if configFilePath != "" {
		config, err = loadConfig(configFilePath)
		if err != nil {
			log.Fatalf("Error loading configuration from file: %v", err)
		}
	} else {
		log.Fatalf("No configuration file provided")
	}
	sess, err = session.NewSession(&aws.Config{
		Region:           aws.String(config.Region),
		Credentials:      credentials.NewStaticCredentials(config.AccessKey, config.SecretKey, ""),
		Endpoint:         aws.String(config.Endpoint),
		S3ForcePathStyle: aws.Bool(true),
	})
	if err != nil {
		log.Fatalf("Error creating session: %v", err)
	}
}

func loadConfig(filePath string) (*Config, error) {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, err
	}

	configFile, err := os.Open(absPath)
	if err != nil {
		return nil, err
	}
	defer configFile.Close()

	var config Config
	if err := json.NewDecoder(configFile).Decode(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

func registerRoutes(router *gin.Engine) {
	router.GET("/keys", listKeys)
	router.GET("/files/:protocol/:network", listFiles)
	router.GET("/files/:protocol/:network/latest", latestSnapshot)
	router.GET("/files/:protocol/:network/info", snapshotInfo)

	// Use the generated docs
	router.NoRoute(ginSwagger.WrapHandler(swaggerFiles.Handler))
}

// @Summary List files in S3 bucket
// @Description Get presigned URLs of files in S3 bucket
// @Accept  json
// @Produce  json
// @Success 200 {object} map[string]string
// @Router /files/{protocol}/{network} [get]
func listFiles(c *gin.Context) {
	protocol := c.Param("protocol")
	network := c.Param("network")

	svc := s3.New(sess)
	req := &s3.ListObjectsV2Input{
		Bucket: aws.String(config.BucketName),
		Prefix: aws.String(fmt.Sprintf("%s/%s/", protocol, network)), // change prefix to match new structure
	}
	resp, err := svc.ListObjectsV2(req)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	files := make([]map[string]interface{}, 0)
	for _, item := range resp.Contents {
		if strings.Contains(*item.Key, protocol) && strings.Contains(*item.Key, network) {
			req, _ := svc.GetObjectRequest(&s3.GetObjectInput{
				Bucket: aws.String(config.BucketName),
				Key:    item.Key,
			})
			urlStr, err := req.Presign(15 * time.Minute)
			if err != nil {
				c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
				return
			}
			file := map[string]interface{}{
				"last_modified": item.LastModified,
				"size":          *item.Size,
				"filename":      *item.Key,
				"url":           urlStr,
			}
			files = append(files, file)
		}
	}

	c.JSON(http.StatusOK, files)
}

func listKeys(c *gin.Context) {
	// Create a session using the default configuration
	sess, _ := session.NewSession()

	// Create a new instance of S3 service
	s3Svc := s3.New(sess)

	// Call S3 to list current objects
	resp, _ := s3Svc.ListObjectsV2(&s3.ListObjectsV2Input{
		Bucket: aws.String(config.BucketName),
	})

	fmt.Printf("ListObjectsV2 response: %+v\n", resp)
	// Prepare a map to hold the unique directories
	dirMap := make(map[string]bool)

	// For each item in the bucket, parse the key and extract directories
	for _, item := range resp.Contents {
		key := *item.Key
		splitKey := strings.Split(key, "/")

		// Check if the key has at least two segments
		if len(splitKey) >= 2 {
			dir := splitKey[0] + "/" + splitKey[1]
			dirMap[dir] = true
		}
	}

	// Convert the map keys to a slice
	dirs := make([]string, 0, len(dirMap))
	for dir := range dirMap {
		dirs = append(dirs, dir)
	}

	// Return the directories as a JSON response
	c.JSON(http.StatusOK, gin.H{"dirs": dirs})
}

func latestSnapshot(c *gin.Context) {
	protocol := c.Param("protocol")
	network := c.Param("network")

	svc := s3.New(sess)
	prefix := fmt.Sprintf("%s/%s/", protocol, network)

	// Assume the files are named with a timestamp as the prefix
	var latestObject *s3.Object

	err := svc.ListObjectsV2Pages(&s3.ListObjectsV2Input{
		Bucket: aws.String(config.BucketName),
		Prefix: aws.String(prefix),
	}, func(page *s3.ListObjectsV2Output, lastPage bool) bool {
		for _, item := range page.Contents {
			if *item.Key != prefix+"snapshot-latest.json" && (latestObject == nil || *item.Key > *latestObject.Key) {
				latestObject = item
			}
		}
		return true // return false to stop iterating
	})

	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	if latestObject == nil {
		c.JSON(http.StatusNotFound, gin.H{"message": "No snapshots found"})
		return
	}

	// Get presigned URL of the latest snapshot
	req, _ := svc.GetObjectRequest(&s3.GetObjectInput{
		Bucket: aws.String(config.BucketName),
		Key:    latestObject.Key,
	})
	urlStr, err := req.Presign(15 * time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{"url": urlStr, "size": *latestObject.Size, "last_modified": latestObject.LastModified})
}

func snapshotInfo(c *gin.Context) {
	protocol := c.Param("protocol")
	network := c.Param("network")

	// Get the snapshot-latest.json
	svc := s3.New(sess)
	result, err := svc.GetObject(&s3.GetObjectInput{
		Bucket: aws.String(config.BucketName),
		Key:    aws.String(fmt.Sprintf("%s/%s/snapshot-latest.json", protocol, network)),
	})
	if err != nil {
		// Cast err to awserr.Error
		if aerr, ok := err.(awserr.Error); ok {
			// If error is due to key not found, respond with default message
			if aerr.Code() == s3.ErrCodeNoSuchKey {
				c.JSON(http.StatusNotFound, gin.H{"message": "Snapshot not found"})
				return
			}
		}
		// If error is of another type, respond with error message
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	defer result.Body.Close()
	body, err := ioutil.ReadAll(result.Body)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	var data interface{}
	err = json.Unmarshal(body, &data)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, data)
}

func main() {
	r := gin.Default()

	// Configure CORS
	config := cors.DefaultConfig()
	config.AllowOrigins = []string{"http://localhost:8080", "http://localhost:8081", "http://cryptosnapshotservice.com", "http://api.cryptoservice.com"}
	r.Use(cors.New(config))

	registerRoutes(r)

	r.Run() // listen and serve on 0.0.0.0:8080
}
