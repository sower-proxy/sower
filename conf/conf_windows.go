// +build windows

package conf

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/golang/glog"
	"github.com/pkg/errors"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/eventlog"
	"golang.org/x/sys/windows/svc/mgr"
)

const name = "sower"
const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown | svc.AcceptPauseAndContinue

func initArgs() {
	cfgFile, _ := filepath.Abs(filepath.Join(filepath.Dir(os.Args[0]), "sower.toml"))
	flag.StringVar(&Conf.ConfigFile, "f", cfgFile, "config file location")
	flag.BoolVar(&Conf.VersionOnly, "V", false, "print sower version")
	install := flag.Bool("install", false, "install sower as a service")
	uninstall := flag.Bool("uninstall", false, "uninstall sower from service list")
	exePath,_:=filepath.Abs(os.Args[0])

	if !flag.Parsed() {
		os.Mkdir("log", 0755)
		flag.Set("log_dir", filepath.Dir(os.Args[0])+"/log")
		flag.Parse()
	}

	switch {
	case *install:
		mgrDo(func(m *mgr.Mgr) error {
			s, err := m.OpenService(name)
			if err == nil {
				s.Close()
				return fmt.Errorf("service %s already exists", name)
			}
			s, err = m.CreateService(name,  exePath, mgr.Config{
				DisplayName: "Sower Proxy",
				StartType:   windows.SERVICE_AUTO_START,
			})
			if err != nil {
				return err
			}
			defer s.Close()
			err = eventlog.InstallAsEventCreate(name, eventlog.Error|eventlog.Warning|eventlog.Info)
			if err != nil {
				s.Delete()
				return fmt.Errorf("SetupEventLogSource() failed: %s", err)
			}

			return s.Start()
		})
		os.Exit(0)

	case *uninstall:
		serviceDo(func(s *mgr.Service) error {
			err := s.Delete()
			if err != nil {
				return err
			}
			return eventlog.Remove(name)
		})
		os.Exit(0)

	default:
		os.Chdir(filepath.Dir(os.Args[0]))
		if active, err := svc.IsAnInteractiveSession(); err != nil {
			glog.Exitf("failed to determine if we are running in an interactive session: %v", err)
		} else if !active {
			go func() {
				elog, err := eventlog.Open(name)
				if err != nil {
					glog.Exitln(err)
				}
				defer elog.Close()

				if err := svc.Run(name, &myservice{}); err != nil {
					elog.Error(1, fmt.Sprintf("%s service failed: %v", name, err))
					glog.Exitln(err)
				}
				elog.Info(1, fmt.Sprintf("winsvc.RunAsService: %s service stopped", name))
				os.Exit(0)
			}()
		}
	}
}

func serviceDo(fn func(*mgr.Service) error) {
	mgrDo(func(m *mgr.Mgr) error {
		s, err := m.OpenService(name)
		if err != nil {
			return fmt.Errorf("could not access service: %v", err)
		}
		defer s.Close()
		return fn(s)
	})
}
func mgrDo(fn func(m *mgr.Mgr) error) {
	m, err := mgr.Connect()
	if err != nil {
		glog.Exitln(err)
	}
	defer m.Disconnect()

	if err := fn(m); err != nil {
		glog.Fatalln(err)
	}
}

type myservice struct{}

func (m *myservice) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (ssec bool, errno uint32) {
	elog, err := eventlog.Open(name)
	if err != nil {
		glog.Errorln(err)
		return
	}
	defer elog.Close()
	elog.Info(1, strings.Join(args, "-"))

	changes <- svc.Status{State: svc.StartPending}
	changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
	for {
		c := <-r
		switch c.Cmd {
		case svc.Interrogate:
			changes <- c.CurrentStatus
			// Testing deadlock from https://code.google.com/p/winsvc/issues/detail?id=4
			time.Sleep(100 * time.Millisecond)
			changes <- c.CurrentStatus
		case svc.Stop, svc.Shutdown:
			changes <- svc.Status{State: svc.StopPending}
			return
		case svc.Pause:
			changes <- svc.Status{State: svc.Paused, Accepts: cmdsAccepted}
		case svc.Continue:
			changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}
		default:
			elog.Error(1, fmt.Sprintf("unexpected control request #%d", c))
		}
	}
}

func execute(cmd string) error {
	ctx, cancel := context.WithTimeout(context.TODO(), 5*time.Second)
	defer cancel()

	var cmds []string
	for _, cmd := range strings.Split(Conf.ClearDNSCache, " ") {
		if cmd == "" {
			continue
		}
		if strings.HasPrefix(cmd, "/") {
			cmd = strings.Replace(cmd, "/", "-", 1)
		}
		cmds = append(cmds, cmd)
	}

	if len(cmds) != 0 {
		return nil
	}

	command := exec.CommandContext(ctx, cmds[0], cmds[1:]...)
	command.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := command.CombinedOutput()
	return errors.Wrapf(err, "cmd: %s, output: %s, error", Conf.ClearDNSCache, out)
}
