package plan

import (
	"context"
	"errors"
	"fmt"
	neturl "net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/hashicorp/go-cleanhttp"
	"github.com/hashicorp/go-getter"
)

// DownloadUpgrade downloads the given url into the provided directory.
// If this returns nil, the download was successful, and {dstRoot}/bin/{daemonName} is a regular executable file.
// This is an opinionated directory structure that corresponds with Cosmovisor requirements.
// If the url is not an archive, it is downloaded and saved to {dstRoot}/bin/{daemonName}.
// If the url is an archive, it is downloaded and unpacked to {dstRoot}.
//
//	If the archive does not contain a /bin/{daemonName} file, then this will attempt to move /{daemonName} to /bin/{daemonName}.
//	If the archive does not contain either /bin/{daemonName} or /{daemonName}, an error is returned.
//
// If dstRoot already exists, some or all of its contents might be updated.
// NOTE: This functions does not check the provided url for validity.
func DownloadUpgrade(dstRoot, url, daemonName string) error {
	target := filepath.Join(dstRoot, "bin", daemonName)

	// First try to download it as a single file. If there's no error, it's okay and we're done.
	if err := getFile(url, target); err != nil {
		// If it was a checksum error, no need to try as directory.
		var checksumError *getter.ChecksumError
		if errors.As(err, &checksumError) {
			return err
		}
		// File download didn't work, try it as an archive.
		if err = downloadUpgradeAsArchive(dstRoot, url, daemonName); err != nil {
			// Out of options, send back the error.
			return err
		}
	}
	return EnsureBinary(target)
}

// downloadUpgradeAsArchive tries to download the given url as an archive.
// The archive is unpacked and saved in dstDir.
// If the archive contains /{daemonName} and not /bin/{daemonName}, then /{daemonName} will be moved to /bin/{daemonName}.
// If this returns nil, the download was successful, and {dstDir}/bin/{daemonName} is a regular executable file.
func downloadUpgradeAsArchive(dstDir, url, daemonName string) error {
	err := getter.Get(dstDir, url)
	if err != nil {
		return err
	}

	// If bin/{daemonName} exists, we're done.
	dstDirBinFile := filepath.Join(dstDir, "bin", daemonName)
	err = EnsureBinary(dstDirBinFile)
	if err == nil {
		return nil
	}

	// Otherwise, check for a root {daemonName} file and move it to the bin/ directory if found.
	dstDirFile := filepath.Join(dstDir, daemonName)
	err = EnsureBinary(dstDirFile)
	if err == nil {
		err = os.Rename(dstDirFile, dstDirBinFile)
		if err != nil {
			return fmt.Errorf("could not move %s to the bin directory: %w", daemonName, err)
		}
		return nil
	}

	return fmt.Errorf("url \"%s\" result does not contain a bin/%s or %s file", url, daemonName, daemonName)
}

// EnsureBinary checks that the given file exists as a regular file and is executable.
// An error is returned if:
//   - The file does not exist.
//   - The path exists, but is one of: Dir, Symlink, NamedPipe, Socket, Device, CharDevice, or Irregular.
//   - The file exists, is not executable by all three of User, Group, and Other, and cannot be made executable.
func EnsureBinary(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.Mode().IsRegular() {
		_, f := filepath.Split(path)
		return fmt.Errorf("%s is not a regular file", f)
	}
	// Make sure all executable bits are set.
	oldMode := info.Mode().Perm()
	newMode := oldMode | 0o111 // Set the three execute bits to on (a+x).
	if oldMode != newMode {
		return os.Chmod(path, newMode)
	}
	return nil
}

// DownloadURL gets the contents of the given url.
// The provided url can contain a checksum parameter that matches the file being downloaded.
// If there isn't an error, the content returned by the url will be returned as a string.
// Returns an error if:
//   - The url is not a URL or does not contain a checksum parameter (when required).
//   - Downloading the URL fails.
//   - The checksum does not match what is returned by the URL.
//   - The URL does not return a regular file.
//   - The downloaded file is empty or only whitespace.
func DownloadURL(url string) (string, error) {
	tempDir, err := os.MkdirTemp("", "reference")
	if err != nil {
		return "", fmt.Errorf("could not create temp directory: %w", err)
	}
	defer os.RemoveAll(tempDir)
	tempFile := filepath.Join(tempDir, "content")

	if err := getFile(url, tempFile); err != nil {
		return "", fmt.Errorf("could not download url \"%s\": %w", url, err)
	}
	tempFileBz, rerr := os.ReadFile(tempFile)
	if rerr != nil {
		return "", fmt.Errorf("could not read downloaded temporary file: %w", rerr)
	}
	tempFileStr := strings.TrimSpace(string(tempFileBz))
	if len(tempFileStr) == 0 {
		return "", fmt.Errorf("no content returned by \"%s\"", url)
	}
	return tempFileStr, nil
}

// ValidateURL checks that the given string is a valid url and optionally contains a checksum query parameter.
func ValidateURL(urlStr string, mustChecksum bool) error {
	url, err := neturl.Parse(urlStr)
	if err != nil {
		return err
	}

	if mustChecksum && len(url.Query().Get("checksum")) == 0 {
		return errors.New("missing checksum query parameter")
	}

	return nil
}

// getFile downloads the given url into the provided directory.
func getFile(url, dst string) error {
	httpGetter := &getter.HttpGetter{
		Client:                cleanhttp.DefaultClient(),
		XTerraformGetDisabled: true,
	}

	goGetterGetters := getter.Getters
	goGetterGetters["http"] = httpGetter
	goGetterGetters["https"] = httpGetter

	// https://github.com/hashicorp/go-getter#security-options
	getterClient := &getter.Client{
		Ctx:             context.Background(),
		DisableSymlinks: true,
		Src:             url,
		Dst:             dst,
		Pwd:             dst,
		Getters:         goGetterGetters,
	}

	return getterClient.Get()
}
