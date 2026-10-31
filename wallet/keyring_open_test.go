//go:build linux

package wallet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unsafe"

	"github.com/cosmos/cosmos-sdk/crypto/hd"
	"github.com/cosmos/cosmos-sdk/crypto/keyring"
	"golang.org/x/sys/unix"

	"github.com/cosmos/cosmos-sdk/codec"
	codectypes "github.com/cosmos/cosmos-sdk/codec/types"
	cryptocodec "github.com/cosmos/cosmos-sdk/crypto/codec"
)

// fakeTTYStdin makes os.Stdin a pseudo-terminal, as a Windows scheduled
// task's hidden console is: the SDK's file prompt then reads the terminal
// and ignores the reader it was given.
func fakeTTYStdin(t *testing.T) {
	t.Helper()
	m, err := os.OpenFile("/dev/ptmx", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("no pty: %v", err)
	}
	var unlock int32
	if _, _, e := unix.Syscall(unix.SYS_IOCTL, m.Fd(), unix.TIOCSPTLCK, uintptr(unsafe.Pointer(&unlock))); e != 0 {
		t.Skipf("unlockpt: %v", e)
	}
	n, err := unix.IoctlGetInt(int(m.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Skipf("ptsname: %v", err)
	}
	s, err := os.OpenFile("/dev/pts/"+itoa(n), os.O_RDWR, 0)
	if err != nil {
		t.Skipf("open pts: %v", err)
	}
	old := os.Stdin
	os.Stdin = s
	t.Cleanup(func() { os.Stdin = old; s.Close(); m.Close() })
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	b := []byte{}
	for ; n > 0; n /= 10 {
		b = append([]byte{byte('0' + n%10)}, b...)
	}
	return string(b)
}

// With the passphrase file set, a file keyring made by the SDK opens even
// when stdin is a terminal, without prompting (it hung on Windows).
func TestNewKeyringPassphraseFileWithTTYStdin(t *testing.T) {
	reg := codectypes.NewInterfaceRegistry()
	cryptocodec.RegisterInterfaces(reg)
	cdc := codec.NewProtoCodec(reg)
	dir := t.TempDir()
	const pass = "relayer-passphrase"
	kr, err := keyring.New("counterpartyd", keyring.BackendFile, dir, strings.NewReader(strings.Repeat(pass+"\n", 4)), cdc)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := kr.NewMnemonic("relayer", keyring.English, hd.CreateHDPath(118, 0, 0).String(), "", hd.Secp256k1); err != nil {
		t.Fatal(err)
	}

	passFile := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(passFile, []byte(pass+"\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(KeyringPassphraseFileEnv, passFile)
	fakeTTYStdin(t)

	open := func() (keyring.Keyring, error) {
		in, err := KeyringInput(keyring.BackendFile)
		if err != nil {
			return nil, err
		}
		return NewKeyring("counterpartyd", keyring.BackendFile, dir, in, cdc)
	}
	done := make(chan error, 1)
	go func() {
		kr, err := open()
		if err == nil {
			_, err = kr.Key("relayer")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("prompted on the terminal instead of using the passphrase file")
	}

	// A wrong passphrase fails at once, without prompting.
	if err := os.WriteFile(passFile, []byte("not-the-passphrase"), 0o600); err != nil {
		t.Fatal(err)
	}
	go func() {
		kr, err := open()
		if err == nil {
			_, err = kr.Key("relayer")
		}
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "incorrect passphrase") {
			t.Fatalf("want incorrect passphrase, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("prompted on a wrong passphrase")
	}
}
