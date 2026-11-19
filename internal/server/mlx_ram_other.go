//go:build !darwin

package server

// ramGB is 0 off-darwin: fusion-mlx model fitting is an Apple Silicon
// feature, so no recommendation is computed on other platforms.
func ramGB() int { return 0 }
