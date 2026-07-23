//go:build !windows

package cmd

func configureTestProcessOwner() error {
	return nil
}
