package wallet

import (
	"bufio"
	"os"
	"path/filepath"
	"testing"

	"github.com/cosmos/cosmos-sdk/crypto/keyring"
)

// The passphrase file must feed the keyring without touching os.Stdin,
// whatever stdin is (closed or invalid under a Windows scheduled task),
// and tolerate a CRLF line ending as PowerShell writes it.
func TestKeyringInputFromFileIgnoresStdin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(path, []byte("correct-horse-battery\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(KeyringPassphraseFileEnv, path)
	old := os.Stdin
	os.Stdin = nil
	defer func() { os.Stdin = old }()
	in, err := KeyringInput(keyring.BackendFile)
	if err != nil {
		t.Fatal(err)
	}
	if in == nil || in == os.Stdin {
		t.Fatal("KeyringInput fell back to stdin")
	}
	br := bufio.NewReader(in)
	for i := 0; i < 3; i++ {
		line, err := br.ReadString('\n')
		if err != nil || line != "correct-horse-battery\n" {
			t.Fatalf("read %d: %q %v", i, line, err)
		}
	}
}
