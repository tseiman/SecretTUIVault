package gpg

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDecryptFeedsExactArmoredBlobAndUsesPinentry(t *testing.T) {
	const armored = "-----BEGIN PGP MESSAGE-----\r\nopaque\x00bytes\r\n-----END PGP MESSAGE-----\r\n"
	runner := Runner{executable: "gpg-test-helper", command: helperCommand(t, "success", armored)}
	result, err := runner.Decrypt(context.Background(), []byte(armored))
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if result.Plaintext != "decrypted text\nsecond line\n" {
		t.Fatalf("plaintext = %q", result.Plaintext)
	}
	if result.Signature != SignatureNone {
		t.Fatalf("signature = %q, want none", result.Signature)
	}
}

func TestDecryptReportsValidSignature(t *testing.T) {
	runner := Runner{executable: "gpg-test-helper", command: helperCommand(t, "valid-signature", "ciphertext")}
	result, err := runner.Decrypt(context.Background(), []byte("ciphertext"))
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if result.Signature != SignatureValid {
		t.Fatalf("signature = %q, want valid", result.Signature)
	}
}

func TestDecryptShowsPlaintextWithInvalidSignatureWarning(t *testing.T) {
	runner := Runner{executable: "gpg-test-helper", command: helperCommand(t, "invalid-signature", "ciphertext")}
	result, err := runner.Decrypt(context.Background(), []byte("ciphertext"))
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if result.Plaintext != "untrusted plaintext" || result.Signature != SignatureInvalid {
		t.Fatalf("result = %#v", result)
	}
}

func TestDecryptRejectsOversizedPlaintext(t *testing.T) {
	runner := Runner{executable: "gpg-test-helper", command: helperCommand(t, "oversized", "ciphertext")}
	_, err := runner.Decrypt(context.Background(), []byte("ciphertext"))
	if !errors.Is(err, errLimitExceeded) {
		t.Fatalf("error = %v, want output limit", err)
	}
}

func TestDecryptRejectsBinaryPlaintext(t *testing.T) {
	runner := Runner{executable: "gpg-test-helper", command: helperCommand(t, "binary", "ciphertext")}
	_, err := runner.Decrypt(context.Background(), []byte("ciphertext"))
	if !errors.Is(err, ErrBinaryPlaintext) {
		t.Fatalf("error = %v, want binary plaintext error", err)
	}
}

func TestDecryptCancellationStopsGPG(t *testing.T) {
	runner := Runner{executable: "gpg-test-helper", command: helperCommand(t, "wait", "ciphertext")}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_, err := runner.Decrypt(ctx, []byte("ciphertext"))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want deadline exceeded", err)
	}
}

func helperCommand(t *testing.T, scenario, expectedInput string) commandFactory {
	t.Helper()
	return func(ctx context.Context, _ string, args ...string) *exec.Cmd {
		cmdArgs := []string{"-test.run=TestGPGHelperProcess", "--", scenario}
		cmdArgs = append(cmdArgs, args...)
		cmd := exec.CommandContext(ctx, os.Args[0], cmdArgs...)
		cmd.Env = append(os.Environ(),
			"SECRET_TUI_GPG_HELPER=1",
			"SECRET_TUI_GPG_INPUT="+base64.StdEncoding.EncodeToString([]byte(expectedInput)),
		)
		return cmd
	}
}

func TestGPGHelperProcess(t *testing.T) {
	if os.Getenv("SECRET_TUI_GPG_HELPER") != "1" {
		return
	}
	separator := -1
	for i, arg := range os.Args {
		if arg == "--" {
			separator = i
			break
		}
	}
	if separator < 0 || separator+2 > len(os.Args) {
		fmt.Fprintln(os.Stderr, "missing helper arguments")
		os.Exit(90)
	}
	scenario := os.Args[separator+1]
	gotArgs := os.Args[separator+2:]
	wantArgs := []string{"--batch", "--no-tty", "--pinentry-mode", "ask", "--status-fd", "2", "--decrypt"}
	if !reflect.DeepEqual(gotArgs, wantArgs) {
		fmt.Fprintf(os.Stderr, "arguments = %q, want %q\n", gotArgs, wantArgs)
		os.Exit(91)
	}
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(92)
	}
	expected, err := base64.StdEncoding.DecodeString(os.Getenv("SECRET_TUI_GPG_INPUT"))
	if err != nil || string(input) != string(expected) {
		fmt.Fprintf(os.Stderr, "stdin mismatch: %q\n", input)
		os.Exit(93)
	}
	switch scenario {
	case "success":
		fmt.Fprintln(os.Stderr, "[GNUPG:] DECRYPTION_OKAY")
		fmt.Fprint(os.Stdout, "decrypted text\nsecond line\n")
	case "valid-signature":
		fmt.Fprintln(os.Stderr, "[GNUPG:] GOODSIG 0123456789ABCDEF Example User")
		fmt.Fprintln(os.Stderr, "[GNUPG:] VALIDSIG 0123456789ABCDEF 2026-08-11 0 4 0 1 10 00 0123456789ABCDEF")
		fmt.Fprintln(os.Stderr, "[GNUPG:] DECRYPTION_OKAY")
		fmt.Fprint(os.Stdout, "signed plaintext")
	case "invalid-signature":
		fmt.Fprintln(os.Stderr, "[GNUPG:] BADSIG 0123456789ABCDEF Example User")
		fmt.Fprintln(os.Stderr, "[GNUPG:] DECRYPTION_OKAY")
		fmt.Fprint(os.Stdout, "untrusted plaintext")
		os.Exit(1)
	case "oversized":
		fmt.Fprintln(os.Stderr, "[GNUPG:] DECRYPTION_OKAY")
		for written := 0; written <= MaxPlaintextSize; written += 1024 {
			_, _ = fmt.Fprint(os.Stdout, strings.Repeat("x", 1024))
		}
	case "binary":
		fmt.Fprintln(os.Stderr, "[GNUPG:] DECRYPTION_OKAY")
		_, _ = os.Stdout.Write([]byte{0xff, 0xfe, 0x00})
	case "wait":
		time.Sleep(30 * time.Second)
	default:
		fmt.Fprintln(os.Stderr, strings.Repeat("unknown scenario ", 2)+scenario)
		os.Exit(94)
	}
	os.Exit(0)
}
