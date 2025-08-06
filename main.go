package main

import (
	"fmt"
	"os"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/imroc/req"
	"github.com/joho/godotenv"
)

func parseFile(files []string) error {
	dirName := "download_" + time.Now().String()
	err := os.Mkdir(dirName, 0777)
	if err == nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	go func() {
		ch := make(chan string, 1)
		for _, url := range files {
			progress := func(current, total int64) {
				fmt.Println(float32(current)/float32(total)*100, "%")
			}

			r := req.New()
			file, err := r.Get(url, req.DownloadProgress(progress))
			if err != nil {
				file.ToFile(dirName + "/" + time.Now().String())
			}
			ch <- file
		}
	}()
	return nil
}

func main() {
	godotenv.Load(".env")

	router := gin.Default()

	router.GET("/", index)

	router.GET("/upload", func(c *gin.Context) {
		files := c.QueryArray("files")
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
		parseFile(files)
		c.JSON(200, gin.H{
			"message": "files uploaded",
		})
	})

	router.GET("/create_zip", createZip)
	router.GET("/download", downloadZip)

	host := fmt.Sprintf("%s:%s", os.Getenv("HOST"), os.Getenv("PORT"))
	router.Run(host)
}

func index(c *gin.Context) {
	c.JSON(200, gin.H{
		"message": "Hello World!",
	})
}
func createZip(c *gin.Context) {
	// Создаем новый архив
}

func downloadZip(c *gin.Context) {
	// Скачиваем архив
}
