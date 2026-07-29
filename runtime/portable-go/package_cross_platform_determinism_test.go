package main

import (
	"bytes"
	"crypto/sha256"
	"os"
	"path/filepath"
	"testing"
)

func rewriteTreeLineEndings(t *testing.T, root string, from, to []byte) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() || info.Name() == "phylang.lock" {
			return nil
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if bytes.Contains(b, []byte{0}) {
			return nil
		}
		return os.WriteFile(path, bytes.ReplaceAll(b, from, to), info.Mode())
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestPackageHashIgnoresCheckoutLineEndings(t *testing.T) {
	lf := filepath.Join(t.TempDir(), "lf")
	crlf := filepath.Join(t.TempDir(), "crlf")
	if err := InitPackage(lf, "community.cross-platform-hash"); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(lf, crlf); err != nil {
		t.Fatal(err)
	}
	rewriteTreeLineEndings(t, crlf, []byte("\n"), []byte("\r\n"))
	lfHash, err := hashPackageDirectory(lf)
	if err != nil {
		t.Fatal(err)
	}
	crlfHash, err := hashPackageDirectory(crlf)
	if err != nil {
		t.Fatal(err)
	}
	if lfHash != crlfHash {
		t.Fatalf("package hash depends on checkout line endings: LF=%s CRLF=%s", lfHash, crlfHash)
	}
}

func TestPackageArchiveIsCrossPlatformDeterministic(t *testing.T) {
	lf := filepath.Join(t.TempDir(), "lf")
	crlf := filepath.Join(t.TempDir(), "crlf")
	if err := InitPackage(lf, "community.cross-platform-archive"); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(lf, crlf); err != nil {
		t.Fatal(err)
	}
	rewriteTreeLineEndings(t, crlf, []byte("\n"), []byte("\r\n"))
	lfArchive := filepath.Join(t.TempDir(), "lf.phypkg")
	crlfArchive := filepath.Join(t.TempDir(), "crlf.phypkg")
	if _, err := PackPackage(lf, lfArchive); err != nil {
		t.Fatal(err)
	}
	if _, err := PackPackage(crlf, crlfArchive); err != nil {
		t.Fatal(err)
	}
	lfBytes, err := os.ReadFile(lfArchive)
	if err != nil {
		t.Fatal(err)
	}
	crlfBytes, err := os.ReadFile(crlfArchive)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(lfBytes, crlfBytes) {
		t.Fatalf("package archives differ across line endings: LF=%x CRLF=%x", sha256.Sum256(lfBytes), sha256.Sum256(crlfBytes))
	}
}

func TestLockContentIsStableAcrossCheckoutLineEndings(t *testing.T) {
	lf := filepath.Join(t.TempDir(), "lf")
	crlf := filepath.Join(t.TempDir(), "crlf")
	if err := InitPackage(lf, "community.cross-platform-lock"); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(lf, crlf); err != nil {
		t.Fatal(err)
	}
	rewriteTreeLineEndings(t, crlf, []byte("\n"), []byte("\r\n"))
	lfManifest, err := LoadPackageManifest(lf)
	if err != nil {
		t.Fatal(err)
	}
	crlfManifest, err := LoadPackageManifest(crlf)
	if err != nil {
		t.Fatal(err)
	}
	lfLock, err := GenerateLock(NewPackageManager(lf), lfManifest)
	if err != nil {
		t.Fatal(err)
	}
	crlfLock, err := GenerateLock(NewPackageManager(crlf), crlfManifest)
	if err != nil {
		t.Fatal(err)
	}
	lfLock.Generated = "fixed"
	crlfLock.Generated = "fixed"
	if !bytes.Equal(mustJSON(t, lfLock), mustJSON(t, crlfLock)) {
		t.Fatalf("lock content differs across line endings: LF=%s CRLF=%s", mustJSON(t, lfLock), mustJSON(t, crlfLock))
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := jsonMarshalIndent(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
