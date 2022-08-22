// Copyright (c) 2020-2022 SMARTX
// All rights reserved

package utils

import (
	"fmt"
	"io/ioutil"
	"time"
)

const (
	retryLimit = 15
	cmdTimeout = 5 * time.Second
)

func Retry(fn func() error, retryLimit int, interval time.Duration) error {
	var err error
	for i := 0; i < retryLimit; i++ {
		err = fn()
		if err == nil {
			break
		}

		time.Sleep(interval)
	}

	return err
}

func ReadFileString(filename string) (string, error) {
	value, err := ioutil.ReadFile(filename)
	if err != nil {
		return "", fmt.Errorf("failed to read %v, %v", filename, err)
	}

	return string(value), nil
}
