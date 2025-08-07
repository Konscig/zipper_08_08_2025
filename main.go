package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/imroc/req"
	"github.com/joho/godotenv"
)

func CheckOrCreateDirMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		dirName := "download_" + uuid.New().String()
		if _, err := os.Stat(dirName); os.IsNotExist(err) {
			err := os.Mkdir(dirName, 0777)
			if err != nil {
				c.JSON(500, gin.H{"error": "failed to create directory"})
				c.Abort()
				return
			}
			c.Set("dirName", dirName)
			c.Next()
		}
	}
}

func downladFiles(files []string, c *gin.Context) error {
	dirName, ok := c.Get("dirName")
	if !ok {
		return fmt.Errorf("failed to get dirName")
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(files))

	for _, url := range files {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			progress := func(current, total int64) {
				fmt.Println(float32(current)/float32(total)*100, "%")
			}

			r := req.New()
			file, err := r.Get(url, req.DownloadProgress(progress))
			if err != nil {
				errCh <- fmt.Errorf("failed to download file %s: %w", url, err)
				return
			}
			fileName := filepath.Base(url)
			savePath := filepath.Join(dirName.(string), fileName)
			err = file.ToFile(savePath)
			if err != nil {
				errCh <- fmt.Errorf("failed to save file: %w", err)
			}
		}(url)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		return err
	}
	return nil
}

func main() {
	godotenv.Load(".env")

	router := gin.Default()

	uploadGroup := router.Group("/", CheckOrCreateDirMiddleware())
	uploadGroup.GET("/upload", func(c *gin.Context) {
		files := c.QueryArray("file")
		if len(files) == 0 {
			c.JSON(400, gin.H{
				"error": "no files uploaded",
			})
			return
		}
		if len(files) > 3 {
			c.JSON(400, gin.H{
				"error": "files count exceeds the limit of 3",
			})
			return
		}
		err := downladFiles(files, c)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"message": "files uploaded",
		})
	})

	router.GET("/create_zip", createZip)
	router.GET("/download", downloadZip)

	host := fmt.Sprintf("%s:%s", os.Getenv("HOST"), os.Getenv("PORT"))
	router.Run(host)
}

func createZip(c *gin.Context) {
	// Создаем новый архив
}

func downloadZip(c *gin.Context) {
	// Скачиваем архив
}
