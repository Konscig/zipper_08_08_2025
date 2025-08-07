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

var allTasks = make(map[string]*Task)
var tasksMutex sync.Mutex
var semaphore = make(chan struct{}, 3)

type Task struct {
	UUID   uuid.UUID `json:"uuid"`
	Files  []string  `json:"files"`
	Status string    `json:"status"`
	IsFull bool      `json:"isFull"`
}

func createSession(c *gin.Context) {
	task := &Task{
		UUID:   uuid.New(),
		Files:  []string{},
		Status: "created",
		IsFull: false,
	}

	dirName := "download_" + task.UUID.String()
	if _, err := os.Stat(dirName); os.IsNotExist(err) {
		err := os.Mkdir(dirName, 0777)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to create directory"})
			return
		}
	}

	tasksMutex.Lock()
	allTasks[task.UUID.String()] = task
	tasksMutex.Unlock()

	c.JSON(200, gin.H{
		"status": task.Status,
		"uuid":   task.UUID.String(),
	})
}

func getStatus(c *gin.Context) {
	uuid := c.Param("uuid")

	tasksMutex.Lock()
	task := allTasks[uuid]
	c.JSON(200, gin.H{
		"uuid":       task.UUID,
		"filesCount": len(task.Files),
		"status":     task.Status,
		"isFull":     task.IsFull,
	})
	tasksMutex.Unlock()
}

func uploadFiles(files []string, c *gin.Context) error {
	uuid := c.Param("uuid")
	dirName := "download_" + uuid

	tasksMutex.Lock()
	task, exists := allTasks[uuid]
	tasksMutex.Unlock()
	if !exists {
		c.JSON(404, gin.H{"error": "task not found"})
		return fmt.Errorf("task with uuid %s not found", uuid)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, len(files))

	for _, url := range files {
		wg.Add(1)
		go func(url string) {
			defer wg.Done()
			r := req.New()
			file, err := r.Get(url)
			if err != nil {
				errCh <- fmt.Errorf("failed to download file %s: %w", url, err)
				return
			}
			fileName := filepath.Base(url)
			savePath := filepath.Join(dirName, fileName)
			err = file.ToFile(savePath)
			if err != nil {
				errCh <- fmt.Errorf("failed to save file: %w", err)
				return
			}

			tasksMutex.Lock()
			task.Files = append(task.Files, savePath)
			tasksMutex.Unlock()
		}(url)
	}
	wg.Wait()
	close(errCh)

	for err := range errCh {
		return err
	}

	tasksMutex.Lock()
	defer tasksMutex.Unlock()
	if len(task.Files) == 3 {
		task.IsFull = true
		task.Status = "ready to zip"
		go func(uuid string) {
			semaphore <- struct{}{}
			defer func() { <-semaphore }()
			err := downloadZip(c)
			if err != nil {
				fmt.Printf("Error creating zip for task %s: %v\n", uuid, err)
			}
		}(uuid)
	}
	return nil
}

func downloadZip(c *gin.Context) error {
	uuid := c.Param("uuid")
	dirName := "download_" + uuid
	zipFileName := dirName + ".zip"

	zip, err := os.Create(zipFileName)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create zip file"})
		return fmt.Errorf("failed to create zip file: %w", err)
	}

	defer zip.Close()
	files, err := os.ReadDir(dirName)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to read directory"})
		return fmt.Errorf("failed to read directory %s: %w", dirName, err)
	}
	for _, file := range files {
		_, err := zip.Write([]byte(file.Name() + "\n"))
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to write to zip file"})
			return fmt.Errorf("failed to write to zip file: %w", err)
		}
	}
	return nil
}

func main() {
	godotenv.Load(".env")

	router := gin.Default()

	uploadGroup := router.Group("/task")

	uploadGroup.GET("/", func(c *gin.Context) {
		createSession(c)
	})

	uploadGroup.GET("/:uuid/upload", func(c *gin.Context) {
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
		err := uploadFiles(files, c)
		if err != nil {
			c.JSON(500, gin.H{"error": err.Error()})
			return
		}
		c.JSON(200, gin.H{
			"message": "files uploaded",
		})
	})
	uploadGroup.GET("/:uuid/status", func(c *gin.Context) {
		getStatus(c)
	})
	uploadGroup.GET("/:uuid/download", func(c *gin.Context) {
		downloadZip(c)
	})
	host := fmt.Sprintf("%s:%s", os.Getenv("HOST"), os.Getenv("PORT"))
	router.Run(host)
}
