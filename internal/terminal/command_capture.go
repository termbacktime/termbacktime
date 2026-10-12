//go:build unix

package terminal

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

type CommandEvent struct {
	ID, Phase, Text, CWD string
	Argv                 []string
	ExitCode             *int
	Partial              bool
}
type ExecutionContext struct{ Shell, Version, CWD string }

const captureInputLimit = 64 << 10

// JSON can expand one input byte to six bytes (for example, a control character).
const captureMessageLimit = 6*captureInputLimit + 1024

func ShellContext(ctx context.Context, shell string, args []string) ExecutionContext {
	cwd, _ := os.Getwd()
	if args != nil {
		return ExecutionContext{Shell: "bash", CWD: cwd}
	}
	if shell == "" {
		shell = os.Getenv("SHELL")
	}
	if shell == "" {
		shell = "/bin/sh"
	}
	name := filepath.Base(shell)
	version := ""
	if name == "bash" || name == "zsh" || name == "fish" {
		probe, cancel := context.WithTimeout(ctx, time.Second)
		defer cancel()
		b, err := exec.CommandContext(probe, shell, "--version").Output()
		if err == nil {
			fields := strings.Fields(string(b))
			for _, f := range fields {
				if len(f) > 0 && f[0] >= '0' && f[0] <= '9' {
					version = strings.Trim(f, ",()")
					if len(version) > 64 {
						version = version[:64]
					}
					break
				}
			}
		}
	}
	return ExecutionContext{Shell: name, Version: version, CWD: cwd}
}
func supported(x ExecutionContext) bool {
	parts := strings.Split(x.Version, ".")
	if len(parts) < 2 {
		return false
	}
	major, _ := strconv.Atoi(parts[0])
	minor, _ := strconv.Atoi(parts[1])
	switch x.Shell {
	case "bash":
		return major > 4 || major == 4 && minor >= 4
	case "zsh":
		return major > 5 || major == 5 && minor >= 8
	case "fish":
		return major > 3 || major == 3 && minor >= 6
	}
	return false
}
func newCommandID() string { var b [16]byte; _, _ = rand.Read(b[:]); return hex.EncodeToString(b[:]) }

type captureMessage struct {
	Token   string `json:"token"`
	Phase   string `json:"phase"`
	PID     int    `json:"pid"`
	Text    string `json:"text"`
	CWD     string `json:"cwd"`
	Code    int    `json:"code"`
	Partial bool   `json:"partial"`
}
type commandSession struct {
	dir, socket, token string
	listener           net.Listener
	ready              chan struct{}
	once               sync.Once
	mu                 sync.Mutex
	connMu             sync.Mutex
	conn               net.Conn
	stopped            bool
	pid                int
	active             string
	emit               func(CommandEvent) error
	done               chan struct{}
	env                []string
	args               []string
	bootstrap          string
}

func captureQuote(v string) string { return "'" + strings.ReplaceAll(v, "'", "'\\''") + "'" }
func prepareCommandSession(ctx context.Context, o Options, emit func(CommandEvent) error) (*commandSession, error) {
	var x ExecutionContext
	if o.CaptureContext != nil {
		x = *o.CaptureContext
	} else {
		x = ShellContext(ctx, o.Shell, o.Args)
	}
	if !supported(x) {
		return nil, fmt.Errorf("command capture unavailable for %s %s; terminal output will still be recorded", x.Shell, x.Version)
	}
	base := os.TempDir()
	// Unix socket paths have a small platform limit, especially on macOS.
	if len(filepath.Join(base, "tbt-capture-0000000000", "events.sock")) > 95 {
		base = "/tmp"
	}
	dir, err := os.MkdirTemp(base, "tbt-capture-")
	if err != nil {
		return nil, err
	}
	s := &commandSession{dir: dir, socket: filepath.Join(dir, "events.sock"), token: newCommandID(), ready: make(chan struct{}), emit: emit}
	s.listener, err = net.Listen("unix", s.socket)
	if err != nil {
		os.RemoveAll(dir)
		return nil, err
	}
	_ = os.Chmod(s.socket, 0600)
	exe, err := os.Executable()
	if o.CaptureExecutable != "" {
		exe = o.CaptureExecutable
		err = nil
	}
	if err != nil {
		s.Close()
		return nil, err
	}
	s.env = []string{"TBT_CAPTURE_SOCKET=" + s.socket, "TBT_CAPTURE_TOKEN=" + s.token, "TBT_CAPTURE_BIN=" + exe}
	initPath := filepath.Join(dir, "init."+x.Shell)
	script := ""
	switch x.Shell {
	case "bash":
		script = bashCapture
		s.bootstrap = " builtin source " + captureQuote(initPath) + "\n"
	case "zsh":
		script = zshCapture
		original, dotdirSet := os.LookupEnv("ZDOTDIR")
		setFlag := "1"
		if !dotdirSet {
			original = os.Getenv("HOME")
			setFlag = "0"
		}
		s.env = append(s.env, "TBT_CAPTURE_REAL_ZDOTDIR="+original, "TBT_CAPTURE_REAL_ZDOTDIR_SET="+setFlag, "ZDOTDIR="+dir)
		for _, file := range []string{".zshenv", ".zprofile", ".zshrc", ".zlogin"} {
			last := file == ".zlogin"
			if o.Args != nil && file == ".zshrc" {
				last = true
			}
			restore := "if [[ $TBT_CAPTURE_REAL_ZDOTDIR_SET == 1 ]]; then ZDOTDIR=$TBT_CAPTURE_REAL_ZDOTDIR; else unset ZDOTDIR; fi\n"
			text := restore + "if [[ -r ${ZDOTDIR-$HOME}/" + file + " ]]; then source \"${ZDOTDIR-$HOME}/" + file + "\"; fi\nif [[ -v ZDOTDIR ]]; then TBT_CAPTURE_REAL_ZDOTDIR=$ZDOTDIR; TBT_CAPTURE_REAL_ZDOTDIR_SET=1; else TBT_CAPTURE_REAL_ZDOTDIR=$HOME; TBT_CAPTURE_REAL_ZDOTDIR_SET=0; fi\n"
			if last {
				text += "source " + captureQuote(initPath) + "\n" + restore
			} else {
				text += "ZDOTDIR=" + captureQuote(dir) + "\n"
			}
			if err = os.WriteFile(filepath.Join(dir, file), []byte(text), 0600); err != nil {
				s.Close()
				return nil, err
			}
		}
	case "fish":
		script = fishCapture
		s.args = []string{"--init-command", "source " + captureQuote(initPath)}
	}
	if err = os.WriteFile(initPath, []byte(script), 0600); err != nil {
		s.Close()
		return nil, err
	}
	s.done = make(chan struct{})
	go s.serve()
	return s, nil
}
func (s *commandSession) serve() {
	defer close(s.done)
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.connMu.Lock()
		if s.stopped {
			s.connMu.Unlock()
			_ = c.Close()
			return
		}
		s.conn = c
		s.connMu.Unlock()
		func() {
			defer c.Close()
			defer func() {
				s.connMu.Lock()
				s.conn = nil
				s.connMu.Unlock()
			}()
			_ = c.SetDeadline(time.Now().Add(time.Second))
			var m captureMessage
			if json.NewDecoder(io.LimitReader(c, captureMessageLimit)).Decode(&m) != nil {
				return
			}
			s.mu.Lock()
			defer s.mu.Unlock()
			if m.Token != s.token || m.PID != s.pid {
				return
			}
			switch m.Phase {
			case "ready":
				s.once.Do(func() { close(s.ready) })
			case "start":
				if m.Text == "" {
					m.Partial = true
				}
				if s.active != "" {
					if s.emit(CommandEvent{ID: s.active, Phase: "end", Partial: true}) != nil {
						return
					}
				}
				s.active = newCommandID()
				if len(m.Text) > 16384 || len(m.CWD) > 16384 {
					m.Text = ""
					m.CWD = ""
					m.Partial = true
				}
				if s.emit(CommandEvent{ID: s.active, Phase: "start", Text: m.Text, CWD: m.CWD, Partial: m.Partial}) != nil {
					return
				}
			case "end":
				if m.Code < 0 || m.Code > 255 {
					return
				}
				if s.active != "" {
					code := m.Code
					if s.emit(CommandEvent{ID: s.active, Phase: "end", ExitCode: &code, Partial: m.Partial || code >= 128}) != nil {
						return
					}
					s.active = ""
				}
			default:
				return
			}
			_, _ = c.Write([]byte("ok\n"))
		}()
	}
}
func (s *commandSession) finish() error {
	// Stop accepting events before closing the final boundary. A missing prompt
	// hook gives no command outcome, even when the outer shell exits successfully.
	s.stop()
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active != "" {
		err := s.emit(CommandEvent{ID: s.active, Phase: "end", Partial: true})
		s.active = ""
		return err
	}
	return nil
}
func (s *commandSession) stop() {
	s.connMu.Lock()
	s.stopped = true
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.connMu.Unlock()
	if s.listener != nil {
		_ = s.listener.Close()
	}
	if s.done != nil {
		<-s.done
	}
}
func (s *commandSession) Close() {
	s.stop()
	if s.dir != "" {
		_ = os.RemoveAll(s.dir)
	}
}

// RunCaptureHelper is a private transport endpoint. It does not interpret or execute its payload.
func RunCaptureHelper(input io.Reader, args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("invalid capture helper arguments")
	}
	pid, err := strconv.Atoi(args[1])
	if err != nil || pid <= 0 {
		return fmt.Errorf("invalid capture process ID")
	}
	if args[0] != "ready" && args[0] != "start" && args[0] != "end" {
		return fmt.Errorf("invalid capture phase")
	}
	token := os.Getenv("TBT_CAPTURE_TOKEN")
	socket := os.Getenv("TBT_CAPTURE_SOCKET")
	if token == "" || socket == "" {
		return fmt.Errorf("capture session is unavailable")
	}
	b, err := io.ReadAll(io.LimitReader(input, captureInputLimit+1))
	if err != nil {
		return err
	}
	partial := len(b) > captureInputLimit
	if partial {
		b = nil
	}
	fields := strings.SplitN(string(b), "\x00", 5)
	m := captureMessage{Token: token, Phase: args[0], PID: pid, Partial: partial}
	if len(fields) > 0 {
		m.Text = fields[0]
	}
	if len(fields) > 1 {
		m.CWD = fields[1]
	}
	if len(fields) > 2 {
		if m.Phase == "end" {
			m.Code, err = strconv.Atoi(fields[2])
			if err != nil || m.Code < 0 || m.Code > 255 {
				return fmt.Errorf("invalid capture exit status")
			}
		}
	} else if m.Phase == "end" {
		return fmt.Errorf("missing capture exit status")
	}
	if len(fields) > 3 {
		m.Partial = m.Partial || fields[3] == "1"
	}
	c, err := net.DialTimeout("unix", socket, 500*time.Millisecond)
	if err != nil {
		return err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(time.Second))
	if err = json.NewEncoder(c).Encode(m); err != nil {
		return err
	}
	var ack [3]byte
	_, err = io.ReadFull(c, ack[:])
	if err == nil && string(ack[:]) != "ok\n" {
		return fmt.Errorf("capture event was not acknowledged")
	}
	return err
}

const bashCapture = `
export TBT_CAPTURE_SHELL_PID=$$
__tbt_send() { builtin printf '%s\0%s\0%s\0%s\0' "$2" "$PWD" "$3" "$4" | "$TBT_CAPTURE_BIN" __capture-event "$1" "$$" 2>/dev/null; }
__tbt_history_number() { local x; x=$(HISTTIMEFORMAT= builtin history 1); x="${x#"${x%%[![:space:]]*}"}"; builtin printf '%s' "${x%% *}"; }
# Remove only the injected initializer from history.
__tbt_init_history=$(__tbt_history_number)
__tbt_init_entry=$(HISTTIMEFORMAT= builtin history 1)
if [[ $__tbt_init_history =~ ^[0-9]+$ && $__tbt_init_entry == *"builtin source "* && $__tbt_init_entry == *"${BASH_SOURCE[0]}"* ]]; then builtin history -d "$__tbt_init_history" 2>/dev/null; fi
__tbt_last_history=$(__tbt_history_number)
__tbt_start() {
 local number text entry partial=0
 entry=$(HISTTIMEFORMAT= builtin history 1)
 # history includes the submitted entry at PS0; fc deliberately excludes it.
 if [[ $entry =~ ^[[:blank:]]*([0-9]+)(.)(.)(.*)$ ]]; then number=${BASH_REMATCH[1]}; text=${BASH_REMATCH[4]}; fi
 if [[ -z $number || $number == "$__tbt_last_history" || -z $text ]]; then text=''; partial=1; fi
 __tbt_send start "$text" '' "$partial"
}
__tbt_end() { local code=$1; __tbt_send end '' "$code" ''; __tbt_last_history=$(__tbt_history_number); return "$code"; }
PS0='$(if [[ $$ == $TBT_CAPTURE_SHELL_PID ]]; then __tbt_start; fi)'"${PS0-}"
if [[ $(declare -p PROMPT_COMMAND 2>/dev/null) == 'declare -a'* ]]; then PROMPT_COMMAND=('case $$ in "$TBT_CAPTURE_SHELL_PID") __tbt_end "$?";; *) (exit "$?");; esac' "${PROMPT_COMMAND[@]}")
else PROMPT_COMMAND='case $$ in "$TBT_CAPTURE_SHELL_PID") __tbt_end "$?";; *) (exit "$?");; esac; '"${PROMPT_COMMAND-}"; fi
__tbt_send ready '' '' ''
builtin printf '\r\033[2K'
`
const zshCapture = `
__tbt_send() { builtin printf '%s\0%s\0%s\0%s\0' "$2" "$PWD" "$3" "$4" | "$TBT_CAPTURE_BIN" __capture-event "$1" "$$" 2>/dev/null }
__tbt_preexec() { local text=$1; [[ -n $text ]] || text=$3; __tbt_send start "$text" '' ''; return 0 }
__tbt_precmd() { local code=$?; __tbt_send end '' "$code" ''; return 0 }
preexec_functions=(__tbt_preexec ${preexec_functions[@]})
precmd_functions=(__tbt_precmd ${precmd_functions[@]})
__tbt_send ready '' '' ''
`
const fishCapture = `
function __tbt_preexec --on-event fish_preexec
    printf '%s\0%s\0' "$argv[1]" "$PWD" | "$TBT_CAPTURE_BIN" __capture-event start $fish_pid 2>/dev/null
end
function __tbt_postexec --on-event fish_postexec
    set -l __tbt_code $status
    printf '\0%s\0%s\0' "$PWD" "$__tbt_code" | "$TBT_CAPTURE_BIN" __capture-event end $fish_pid 2>/dev/null
end
function __tbt_posterror --on-event fish_posterror
    printf '%s\0%s\0%s\0%s\0' "$argv[1]" "$PWD" 2 1 | "$TBT_CAPTURE_BIN" __capture-event start $fish_pid 2>/dev/null
    printf '\0%s\0%s\0%s\0' "$PWD" 2 1 | "$TBT_CAPTURE_BIN" __capture-event end $fish_pid 2>/dev/null
end
printf '' | "$TBT_CAPTURE_BIN" __capture-event ready $fish_pid 2>/dev/null
`
