package main

import (
	"archive/zip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

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

// checkContentType проверяет тип скачиваемого файла.
//
// Параметры:
//   - c: указатель на контекст Gin (*gin.Context).
//
// Возвращает:
//   - nil (Если тип файла удовлетворительный).
//   - ошибку, если формат неверный.
func checkContentType(c *gin.Context) error {
	types := []string{"application/pdf", "image/jpeg"}

	url := c.Query("file")
	if url == "" {
		c.JSON(400, gin.H{"error": "file parameter is required"})
		return fmt.Errorf("file parameter is missing")
	}

	resp, err := req.Head(url)
	if err != nil {
		c.JSON(400, gin.H{"error": "head request failed"})
		return fmt.Errorf("head request failed: %w", err)
	}
	if resp.Response().StatusCode != 200 {
		c.JSON(400, gin.H{"error": "file not found"})
		return fmt.Errorf("file not found: %w", err)
	}

	contentType := resp.Response().Header.Get("Content-Type")
	for _, t := range types {
		if strings.HasPrefix(contentType, t) {
			return nil
		}
	}

	c.JSON(415, gin.H{"error": "unsupported file type", "got": contentType})
	return fmt.Errorf("unsupported file type: %s", contentType)
}

// createSession создает сессию для загрузки файлов.
//
// Параметры:
//   - c: указатель на контекст Gin (*gin.Context).
//
// Возвращает:
//   - nil.
//   - err если возникла ошибка загрузки.
func createSession(c *gin.Context) {
	if len(allTasks) < 3 {
		semaphore <- struct{}{}
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
	} else {
		c.JSON(503, gin.H{
			"error": "maximum number of tasks reached",
		})
	}
}

// getStatus возвращает статус задачи по её UUID.
//
// Параметры:
//   - c: указатель на контекст Gin (*gin.Context).
//
// Возвращает:
//
//	json с информацией о задаче, включая UUID, количество файлов и статус задачи.
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

// uploadFiles загружает файлы в каталог задачи по её UUID.
//
// Параметры:
//   - c: указатель на контекст Gin (*gin.Context).
//
// Возвращает:
//
//   - nil (Если файлы успешно загружены).
//   - ошибку, если возникла ошибка загрузки или задача уже заполнена.
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

	tasksMutex.Lock()
	if task.IsFull {
		c.JSON(400, gin.H{"error": "task is already full"})
		return fmt.Errorf("task with uuid %s is already full", uuid)
	}
	tasksMutex.Unlock()

	var wg sync.WaitGroup
	errCh := make(chan error, len(files))

	for _, url := range files {
		if err := checkContentType(c); err != nil {
			errCh <- err
			continue
		}
		wg.Add(1)
		go func(url string) {
			defer wg.Done()

			tasksMutex.Lock()
			if len(task.Files) >= 3 {
				tasksMutex.Unlock()
				errCh <- fmt.Errorf("task with uuid %s is already full", uuid)
				return
			}
			tasksMutex.Unlock()

			r := req.New()
			file, err := r.Get(url)
			if err != nil {
				errCh <- fmt.Errorf("failed to download file %s: %w", url, err)
				return
			}
			fileName := filepath.Base(url)
			ext := filepath.Ext(fileName)
			base := strings.TrimSuffix(fileName, ext)
			savePath := filepath.Join(dirName, fileName)
			i := 1

			for {
				if _, err := os.Stat(savePath); os.IsNotExist(err) {
					break
				}
				fileName = fmt.Sprintf("%s_(%d)%s", base, i, ext)
				savePath = filepath.Join(dirName, fileName)
				i++
			}

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
		go func() {
			defer func() { <-semaphore }()
			task.IsFull = true
			task.Status = "full"
		}()
		c.Redirect(302, "/task/"+task.UUID.String()+"/download")
	}
	return nil
}

// removeFiles удаляет файлы и задачу по её UUID.
//
// Параметры:
//   - c: указатель на контекст Gin (*gin.Context).
//
// Возвращает:
//
//   - nil.
//   - ошибку, если возникла ошибка удаления файлов или задачи.
func removeFiles(c *gin.Context) {
	uuid := c.Param("uuid")
	dirName := "download_" + uuid

	tasksMutex.Lock()
	delete(allTasks, uuid)
	tasksMutex.Unlock()

	os.RemoveAll(dirName)
	c.JSON(200, gin.H{
		"message": "files removed successfully",
	})
}

// downloadZip формирует zip и отправляет его пользователю.
//
// Параметры:
//   - c: указатель на контекст Gin (*gin.Context).
//
// Возвращает:
//
//   - nil.
//   - ошибку, если возникла ошибка при создании zip-файла или чтении директории.
func downloadZip(c *gin.Context) error {
	uuid := c.Param("uuid")
	dirName := "download_" + uuid
	zipFileName := dirName + ".zip"
	zipFile, err := os.Create(zipFileName)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to create zip file"})
		return fmt.Errorf("failed to create zip file: %w", err)
	}
	defer zipFile.Close()

	zipWriter := zip.NewWriter(zipFile)
	defer zipWriter.Close()
	files, err := os.ReadDir(dirName)
	if err != nil {
		c.JSON(500, gin.H{"error": "failed to read directory"})
		return fmt.Errorf("failed to read directory %s: %w", dirName, err)
	}

	for _, file := range files {
		filePath := filepath.Join(dirName, file.Name())
		srcFile, err := os.Open(filePath)
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to open file: " + file.Name()})
			return fmt.Errorf("failed to open file: %w", err)
		}
		zipEntry, err := zipWriter.Create(file.Name())
		if err != nil {
			srcFile.Close()
			c.JSON(500, gin.H{"error": "failed to create zip entry for file: " + file.Name()})
			return fmt.Errorf("failed to create zip entry: %w", err)
		}
		_, err = io.Copy(zipEntry, srcFile)
		srcFile.Close()
		if err != nil {
			c.JSON(500, gin.H{"error": "failed to write file to zip: " + file.Name()})
			return fmt.Errorf("failed to write file to zip: %w", err)
		}
	}
	tasksMutex.Lock()
	defer tasksMutex.Unlock()
	if task, exists := allTasks[uuid]; exists {
		task.Status = "done"
	}

	zipWriter.Close()
	c.FileAttachment(zipFileName, filepath.Base(zipFileName))
	go removeFiles(c)

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
		uploadFiles(files, c)
	})
	uploadGroup.GET("/:uuid/status", func(c *gin.Context) {
		getStatus(c)
	})
	uploadGroup.GET("/:uuid/download", func(c *gin.Context) {
		defer func() {
			<-semaphore
		}()

		err := downloadZip(c)
		if err != nil {
			fmt.Printf("Error downloading zip: %v\n", err)
			return
		}

		go func() {
			time.Sleep(15 * time.Second)
			removeFiles(c)
		}()

		c.JSON(200, gin.H{
			"message": "zip file created successfully. downloading will start automatically. files will be removed from the server in 15 sec.",
		})
	})
	host := fmt.Sprintf("%s:%s", os.Getenv("HOST"), os.Getenv("PORT"))
	router.Run(host)
}
