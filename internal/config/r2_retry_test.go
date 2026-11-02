package config

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/minio/minio-go/v7"
)

// 本文件验证 R2 的「幂等操作重试一次」策略：只对 5xx/429/408/网络超时/提前断开重试，
// 业务错误（403/404 等）和 ctx 取消不重试，避免把一次失败放大成两次。

type r2TimeoutError struct{}

func (r2TimeoutError) Error() string   { return "i/o timeout" }
func (r2TimeoutError) Timeout() bool   { return true }
func (r2TimeoutError) Temporary() bool { return true }

func TestIsRetryableR2Error(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context-canceled", context.Canceled, false},
		{"context-deadline", context.DeadlineExceeded, false},
		{"server-503", minio.ErrorResponse{StatusCode: 503}, true},
		{"server-500", minio.ErrorResponse{StatusCode: 500}, true},
		{"server-429", minio.ErrorResponse{StatusCode: 429}, true},
		{"server-408", minio.ErrorResponse{StatusCode: 408}, true},
		{"server-403", minio.ErrorResponse{StatusCode: 403}, false},
		{"server-404", minio.ErrorResponse{StatusCode: 404}, false},
		{"net-timeout", r2TimeoutError{}, true},
		{"unexpected-eof", io.ErrUnexpectedEOF, true},
		{"plain", errors.New("boom"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isRetryableR2Error(tc.err); got != tc.want {
				t.Fatalf("isRetryableR2Error(%v)=%v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestRunR2WithRetryRetriesTransientErrorOnce(t *testing.T) {
	attempts := 0
	err := runR2WithRetry("test", func() error {
		attempts++
		if attempts < 2 {
			return minio.ErrorResponse{StatusCode: 503}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("重试后应成功: %v", err)
	}
	if attempts != 2 {
		t.Fatalf("瞬时错误应重试一次，实际尝试 %d 次", attempts)
	}
}

func TestRunR2WithRetryDoesNotRetryBusinessError(t *testing.T) {
	attempts := 0
	err := runR2WithRetry("test", func() error {
		attempts++
		return minio.ErrorResponse{StatusCode: 403}
	})
	if err == nil {
		t.Fatal("403 应返回错误")
	}
	if attempts != 1 {
		t.Fatalf("业务错误不应重试，实际尝试 %d 次", attempts)
	}
}

func TestRunR2WithRetryGivesUpAfterSecondAttempt(t *testing.T) {
	attempts := 0
	err := runR2WithRetry("test", func() error {
		attempts++
		return minio.ErrorResponse{StatusCode: 503}
	})
	if err == nil {
		t.Fatal("持续 503 最终应返回错误")
	}
	if attempts != 2 {
		t.Fatalf("持续瞬时错误最多重试一次（共 2 次），实际 %d 次", attempts)
	}
}
