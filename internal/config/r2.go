package config

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

type R2Config struct {
	AccessKeyID     string `json:"accessKeyId"`
	SecretAccessKey string `json:"secretAccessKey"`
	Bucket          string `json:"bucket"`
	Endpoint        string `json:"endpoint"`
	Region          string `json:"region"`
	Prefix          string `json:"prefix"`
	MaxBackups      int    `json:"maxBackups"`
}

func (c *ConfigManager) GetR2Config() *R2Config {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.getR2ConfigLocked()
}

// getR2ConfigLocked 读取 R2 配置，调用方需持有 c.mu
func (c *ConfigManager) getR2ConfigLocked() *R2Config {
	r2File := filepath.Join(c.configDir, "r2.json")
	data, err := os.ReadFile(r2File)
	if err != nil {
		return nil
	}
	var conf R2Config
	if err := json.Unmarshal(data, &conf); err != nil {
		return nil
	}
	conf.AccessKeyID = c.decrypt(conf.AccessKeyID)
	conf.SecretAccessKey = c.decrypt(conf.SecretAccessKey)
	if conf.Region == "" {
		conf.Region = "auto"
	}
	if conf.Prefix == "" {
		conf.Prefix = "LumeTerm/"
	}
	return &conf
}

func (c *ConfigManager) SaveR2Config(config map[string]string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	existing := c.getR2ConfigLocked()

	accessKey := config["accessKeyId"]
	secretKey := config["secretAccessKey"]
	if accessKey == "" && existing != nil {
		accessKey = existing.AccessKeyID
	}
	if secretKey == "" && existing != nil {
		secretKey = existing.SecretAccessKey
	}

	prefix := config["prefix"]
	if prefix == "" {
		prefix = "LumeTerm/"
	}
	if prefix[len(prefix)-1] != '/' {
		prefix += "/"
	}

	region := config["region"]
	if region == "" {
		region = "auto"
	}

	maxBackups := parseIntOrDefault(config["maxBackups"], 0)

	conf := R2Config{
		Bucket:     config["bucket"],
		Endpoint:   sanitizeEndpoint(config["endpoint"]),
		Region:     region,
		Prefix:     prefix,
		MaxBackups: maxBackups,
	}

	encKey, err := c.encrypt(accessKey)
	if err != nil {
		return fmt.Errorf("encrypt access key: %w", err)
	}
	encSecret, err := c.encrypt(secretKey)
	if err != nil {
		return fmt.Errorf("encrypt secret key: %w", err)
	}
	conf.AccessKeyID = encKey
	conf.SecretAccessKey = encSecret
	r2File := filepath.Join(c.configDir, "r2.json")
	data, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal r2 config: %w", err)
	}
	return atomicWriteFile(r2File, data, 0600)
}

// sanitizeEndpoint 去除 URL 中的协议前缀和尾部斜杠，minio.New 会自动拼接 https://
func sanitizeEndpoint(endpoint string) string {
	e := strings.TrimSpace(endpoint)
	e = strings.TrimSuffix(e, "/")
	e = strings.TrimPrefix(e, "https://")
	e = strings.TrimPrefix(e, "http://")
	return e
}

func (c *ConfigManager) TestR2Connection(accessKeyId, secretAccessKey, bucket, endpoint string) error {
	cli, err := minio.New(sanitizeEndpoint(endpoint), &minio.Options{
		Creds:  credentials.NewStaticV4(accessKeyId, secretAccessKey, ""),
		Secure: true,
		Region: "auto",
	})
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for obj := range cli.ListObjects(ctx, bucket, minio.ListObjectsOptions{
		Prefix:  "",
		MaxKeys: 1,
	}) {
		if obj.Err != nil {
			return obj.Err
		}
	}
	return nil
}

func (c *ConfigManager) newR2Client() (*minio.Client, error) {
	conf := c.GetR2Config()
	if conf == nil {
		return nil, fmt.Errorf("R2 not configured")
	}
	return minio.New(sanitizeEndpoint(conf.Endpoint), &minio.Options{
		Creds:  credentials.NewStaticV4(conf.AccessKeyID, conf.SecretAccessKey, ""),
		Secure: true,
		Region: conf.Region,
	})
}

// ─── R2 RemoteStorage 实现 ─────────────────────────────────

type r2Storage struct {
	cli        *minio.Client
	bucket     string
	prefix     string
	maxBackups int
}

func (s *r2Storage) MaxBackups() int { return s.maxBackups }

func (s *r2Storage) ListFiles() ([]RemoteFile, error) {
	// R2 操作加 30s 超时，避免网络挂起时永久阻塞
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	objects := s.cli.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Prefix: s.prefix})
	var result []RemoteFile
	var firstErr error
	for obj := range objects {
		if obj.Err != nil {
			if firstErr == nil {
				firstErr = obj.Err
			}
			continue
		}
		name := strings.TrimPrefix(obj.Key, s.prefix)
		if name == "" {
			continue
		}
		result = append(result, RemoteFile{
			Name:    name,
			ModTime: obj.LastModified,
			Size:    obj.Size,
		})
	}
	if firstErr != nil {
		return nil, fmt.Errorf("list objects error: %w", firstErr)
	}
	return result, nil
}

func (s *r2Storage) ReadFile(name string) ([]byte, error) {
	// R2 操作加 30s 超时，避免网络挂起时永久阻塞
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	obj, err := s.cli.GetObject(ctx, s.bucket, s.prefix+name, minio.GetObjectOptions{})
	if err != nil {
		return nil, err
	}
	defer obj.Close()
	buf := new(bytes.Buffer)
	_, err = buf.ReadFrom(obj)
	return buf.Bytes(), err
}

func (s *r2Storage) WriteFile(name string, data []byte) error {
	// R2 操作加 30s 超时，避免网络挂起时永久阻塞
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	objectKey := s.prefix + name
	_, err := s.cli.PutObject(ctx, s.bucket, objectKey, bytes.NewReader(data), int64(len(data)), minio.PutObjectOptions{
		ContentType: "application/octet-stream",
	})
	return err
}

func (s *r2Storage) DeleteFile(name string) error {
	// R2 操作加 30s 超时，避免网络挂起时永久阻塞
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	return s.cli.RemoveObject(ctx, s.bucket, s.prefix+name, minio.RemoveObjectOptions{})
}

func (c *ConfigManager) newR2Storage() (RemoteStorage, int, error) {
	conf := c.GetR2Config()
	if conf == nil {
		return nil, 0, fmt.Errorf("R2 not configured")
	}
	cli, err := c.newR2Client()
	if err != nil {
		return nil, 0, err
	}
	return &r2Storage{cli: cli, bucket: conf.Bucket, prefix: conf.Prefix, maxBackups: conf.MaxBackups}, conf.MaxBackups, nil
}

// BackupToR2 备份到 R2
func (c *ConfigManager) BackupToR2() (map[string]interface{}, error) {
	return c.backupTo("r2", c.newR2Storage)
}

// ListR2Backups 列出 R2 备份
func (c *ConfigManager) ListR2Backups() ([]map[string]interface{}, error) {
	return c.listBackupsFrom(c.newR2Storage)
}

// SyncFromR2 手动合并同步
func (c *ConfigManager) SyncFromR2() (map[string]interface{}, error) {
	return c.syncFrom("r2", c.newR2Storage)
}

func (c *ConfigManager) RestoreFromR2File(objectKey string) (map[string]interface{}, error) {
	return c.restoreFrom("r2", c.newR2Storage, objectKey, c.GetRecoveryPassword())
}

// RestoreFromR2FileWithPassword 用用户输入的密码恢复（恢复失败时的兜底入口）。
func (c *ConfigManager) RestoreFromR2FileWithPassword(objectKey string, password string) (map[string]interface{}, error) {
	return c.restoreFrom("r2", c.newR2Storage, objectKey, password)
}
