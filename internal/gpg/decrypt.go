package gpg

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"unicode/utf8"
)

const MaxPlaintextSize = 8 << 20
const maxDiagnosticSize = 64 << 10

type SignatureStatus string

const (
	SignatureNone       SignatureStatus = "none"
	SignatureValid      SignatureStatus = "valid"
	SignatureInvalid    SignatureStatus = "invalid"
	SignatureUnverified SignatureStatus = "unverified"
)

type Result struct {
	Plaintext string
	Signature SignatureStatus
}

var ErrBinaryPlaintext = errors.New("GPG produced binary or non-UTF-8 plaintext")
var errLimitExceeded = errors.New("decrypted output exceeds 8 MiB limit")

type commandFactory func(context.Context, string, ...string) *exec.Cmd

type Runner struct {
	executable string
	command    commandFactory
}

func NewRunner() Runner {
	return Runner{executable: "gpg", command: exec.CommandContext}
}

func (r Runner) Decrypt(ctx context.Context, armored []byte) (Result, error) {
	if r.executable == "" {
		r.executable = "gpg"
	}
	if r.command == nil {
		r.command = exec.CommandContext
	}
	cmd := r.command(ctx, r.executable,
		"--batch",
		"--no-tty",
		"--pinentry-mode", "ask",
		"--status-fd", "2",
		"--decrypt",
	)
	cmd.Stdin = bytes.NewReader(armored)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open GPG plaintext output: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{}, fmt.Errorf("open GPG diagnostic output: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return Result{}, fmt.Errorf("start GPG: %w", err)
	}

	type readResult struct {
		data []byte
		err  error
	}
	plaintextCh := make(chan readResult, 1)
	diagnosticCh := make(chan readResult, 1)
	readPipe := func(reader io.Reader, limit int64, output chan<- readResult) {
		data, readErr := readLimited(reader, limit)
		if errors.Is(readErr, errLimitExceeded) && cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		output <- readResult{data: data, err: readErr}
	}
	go readPipe(stdout, MaxPlaintextSize, plaintextCh)
	go readPipe(stderr, maxDiagnosticSize, diagnosticCh)
	plaintextRead := <-plaintextCh
	diagnosticRead := <-diagnosticCh
	waitErr := cmd.Wait()

	if plaintextRead.err != nil {
		return Result{}, plaintextRead.err
	}
	if diagnosticRead.err != nil {
		return Result{}, errors.New("GPG diagnostic output exceeded the safety limit")
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return Result{}, ctxErr
	}
	status := parseStatus(diagnosticRead.data)
	if waitErr != nil && !status.decryptionOkay {
		return Result{}, decryptionError(status, waitErr)
	}
	if !status.decryptionOkay {
		return Result{}, errors.New("GPG did not confirm successful decryption")
	}
	if !utf8.Valid(plaintextRead.data) || bytes.IndexByte(plaintextRead.data, 0) >= 0 {
		return Result{}, ErrBinaryPlaintext
	}
	return Result{Plaintext: string(plaintextRead.data), Signature: status.signature}, nil
}

type statusResult struct {
	decryptionOkay bool
	noSecretKey    bool
	badPassphrase  bool
	signature      SignatureStatus
}

func parseStatus(diagnostics []byte) statusResult {
	status := statusResult{signature: SignatureNone}
	for _, line := range strings.Split(string(diagnostics), "\n") {
		fields := strings.Fields(strings.TrimSpace(line))
		if len(fields) < 2 || fields[0] != "[GNUPG:]" {
			continue
		}
		switch fields[1] {
		case "DECRYPTION_OKAY":
			status.decryptionOkay = true
		case "NO_SECKEY":
			status.noSecretKey = true
		case "BAD_PASSPHRASE":
			status.badPassphrase = true
		case "VALIDSIG":
			if status.signature == SignatureNone {
				status.signature = SignatureValid
			}
		case "BADSIG":
			status.signature = SignatureInvalid
		case "ERRSIG", "NO_PUBKEY", "EXPSIG", "EXPKEYSIG", "REVKEYSIG":
			if status.signature != SignatureInvalid {
				status.signature = SignatureUnverified
			}
		}
	}
	return status
}

func decryptionError(status statusResult, waitErr error) error {
	switch {
	case status.noSecretKey:
		return errors.New("GPG has no matching secret key")
	case status.badPassphrase:
		return errors.New("GPG rejected the passphrase")
	default:
		return fmt.Errorf("GPG decryption failed: %w", waitErr)
	}
}

func readLimited(reader io.Reader, limit int64) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, errLimitExceeded
	}
	return data, nil
}
