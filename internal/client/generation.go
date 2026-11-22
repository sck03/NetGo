package client

import (
	"errors"
	"math"
	"netgo/internal/securefile"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

func nextGeneration(root string, current uint64) (uint64, error) {
	path := filepath.Join(root, ".generation")
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return 0, err
	}
	if err == nil {
		if len(b) == 0 || len(b) > 20 || strings.Trim(string(b), "0123456789") != "" {
			return 0, errors.New("invalid persisted generation")
		}
		saved, err := strconv.ParseUint(string(b), 10, 64)
		if err != nil {
			return 0, err
		}
		if saved > current {
			current = saved
		}
	}
	if current == math.MaxUint64 {
		return 0, errors.New("generation exhausted")
	}
	current++
	if err = os.MkdirAll(root, 0700); err != nil {
		return 0, err
	}
	if err = securefile.Write(path, []byte(strconv.FormatUint(current, 10)), 0600); err != nil {
		return 0, err
	}
	return current, nil
}
