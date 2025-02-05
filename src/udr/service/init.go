package service

import (
	"bufio"
	"fmt"
	"free5gc/lib/http2_util"
	"free5gc/lib/path_util"
	"free5gc/src/app"
	"free5gc/src/udr/consumer"
	udr_context "free5gc/src/udr/context"
	"free5gc/src/udr/datarepository"
	"free5gc/src/udr/factory"
	"free5gc/src/udr/handler"
	"free5gc/src/udr/logger"
	"free5gc/src/udr/util"
	"os/exec"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

type UDR struct{}

type (
	// Config information.
	Config struct {
		udrcfg string
	}
)

var config Config

var udrCLi = []cli.Flag{
	cli.StringFlag{
		Name:  "free5gccfg",
		Usage: "common config file",
	},
	cli.StringFlag{
		Name:  "udrcfg",
		Usage: "config file",
	},
}

var initLog *logrus.Entry

func init() {
	initLog = logger.InitLog
}

func (*UDR) GetCliCmd() (flags []cli.Flag) {
	return udrCLi
}

func (*UDR) Initialize(c *cli.Context) {

	config = Config{
		udrcfg: c.String("udrcfg"),
	}

	if config.udrcfg != "" {
		factory.InitConfigFactory(config.udrcfg)
	} else {
		DefaultUdrConfigPath := path_util.Gofree5gcPath("free5gc/config/udrcfg.conf")
		factory.InitConfigFactory(DefaultUdrConfigPath)
	}

	if app.ContextSelf().Logger.UDR.DebugLevel != "" {
		level, err := logrus.ParseLevel(app.ContextSelf().Logger.UDR.DebugLevel)
		if err != nil {
			initLog.Warnf("Log level [%s] is not valid, set to [info] level", app.ContextSelf().Logger.UDR.DebugLevel)
			logger.SetLogLevel(logrus.InfoLevel)
		} else {
			logger.SetLogLevel(level)
			initLog.Infof("Log level is set to [%s] level", level)
		}
	} else {
		initLog.Infoln("Log level is default set to [info] level")
		logger.SetLogLevel(logrus.InfoLevel)
	}

	logger.SetReportCaller(app.ContextSelf().Logger.UDR.ReportCaller)

}

func (udr *UDR) FilterCli(c *cli.Context) (args []string) {
	for _, flag := range udr.GetCliCmd() {
		name := flag.GetName()
		value := fmt.Sprint(c.Generic(name))
		if value == "" {
			continue
		}

		args = append(args, "--"+name, value)
	}
	return args
}

func (udr *UDR) Start() {
	// get config file info
	config := factory.UdrConfig
	mongodb := config.Configuration.Mongodb

	initLog.Infof("UDR Config Info: Version[%s] Description[%s]", config.Info.Version, config.Info.Description)

	// Connect to MongoDB
	datarepository.SetMongoDB(mongodb.Name, mongodb.Url)

	initLog.Infoln("Server started")

	router := gin.Default()

	datarepository.AddService(router)

	udrLogPath := util.UdrLogPath
	udrPemPath := util.UdrPemPath
	udrKeyPath := util.UdrKeyPath

	self := udr_context.UDR_Self()
	util.InitUdrContext(self)

	addr := fmt.Sprintf("%s:%d", self.HttpIPv4Address, self.HttpIpv4Port)
	profile := consumer.BuildNFInstance(self)
	var newNrfUri string
	var err error
	newNrfUri, self.NfId, err = consumer.SendRegisterNFInstance(self.NrfUri, profile.NfInstanceId, profile)
	if err == nil {
		self.NrfUri = newNrfUri
	} else {
		initLog.Errorf("Send Register NFInstance Error[%s]", err.Error())
	}

	go handler.Handle()
	server, err := http2_util.NewServer(addr, udrLogPath, router)
	if server == nil {
		initLog.Errorln("Initialize HTTP server failed: %+v", err)
		return
	}

	if err != nil {
		initLog.Warnln("Initialize HTTP server: +%v", err)
	}

	serverScheme := factory.UdrConfig.Configuration.Sbi.Scheme
	if serverScheme == "http" {
		err = server.ListenAndServe()
	} else if serverScheme == "https" {
		err = server.ListenAndServeTLS(udrPemPath, udrKeyPath)
	}

	if err != nil {
		initLog.Fatalln("HTTP server setup failed: %+v", err)
	}
}

func (udr *UDR) Exec(c *cli.Context) error {

	//UDR.Initialize(cfgPath, c)

	initLog.Traceln("args:", c.String("udrcfg"))
	args := udr.FilterCli(c)
	initLog.Traceln("filter: ", args)
	command := exec.Command("./udr", args...)

	udr.Initialize(c)

	stdout, err := command.StdoutPipe()
	if err != nil {
		initLog.Fatalln(err)
	}
	wg := sync.WaitGroup{}
	wg.Add(3)
	go func() {
		in := bufio.NewScanner(stdout)
		for in.Scan() {
			fmt.Println(in.Text())
		}
		wg.Done()
	}()

	stderr, err := command.StderrPipe()
	if err != nil {
		initLog.Fatalln(err)
	}
	go func() {
		in := bufio.NewScanner(stderr)
		for in.Scan() {
			fmt.Println(in.Text())
		}
		wg.Done()
	}()

	go func() {
		if err := command.Start(); err != nil {
			fmt.Println("command.Start Fails!")
		}
		wg.Done()
	}()

	wg.Wait()

	return err
}
