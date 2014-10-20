package main

import (
	"flag"
	"os"
	"strings"
	"time"

	"github.com/cloudfoundry-incubator/cf-debug-server"
	"github.com/cloudfoundry-incubator/cf-lager"
	"github.com/cloudfoundry-incubator/converger/converger_process"
	"github.com/cloudfoundry-incubator/converger/lrpreprocessor"
	"github.com/cloudfoundry-incubator/converger/lrpwatcher"
	Bbs "github.com/cloudfoundry-incubator/runtime-schema/bbs"
	"github.com/cloudfoundry-incubator/runtime-schema/bbs/lock_bbs"
	_ "github.com/cloudfoundry/dropsonde/autowire"
	"github.com/cloudfoundry/gunk/timeprovider"
	"github.com/cloudfoundry/storeadapter/etcdstoreadapter"
	"github.com/cloudfoundry/storeadapter/workerpool"
	"github.com/nu7hatch/gouuid"
	"github.com/pivotal-golang/lager"
	"github.com/tedsuo/ifrit"
	"github.com/tedsuo/ifrit/grouper"
	"github.com/tedsuo/ifrit/sigmon"
)

var etcdCluster = flag.String(
	"etcdCluster",
	"http://127.0.0.1:4001",
	"comma-separated list of etcd addresses (http://ip:port)",
)

var heartbeatInterval = flag.Duration(
	"heartbeatInterval",
	lock_bbs.HEARTBEAT_INTERVAL,
	"the interval between heartbeats to the lock",
)

var convergeRepeatInterval = flag.Duration(
	"convergeRepeatInterval",
	30*time.Second,
	"the interval between runs of the converge process",
)

var kickPendingTaskDuration = flag.Duration(
	"kickPendingTaskDuration",
	30*time.Second,
	"the interval, in seconds, between kicks to pending tasks",
)

var expirePendingTaskDuration = flag.Duration(
	"expirePendingTaskDuration",
	30*time.Minute,
	"unclaimed tasks are marked as failed, after this duration",
)

var expireCompletedTaskDuration = flag.Duration(
	"expireCompletedTaskDuration",
	60*time.Minute,
	"unresolved tasks are deleted, after this duration",
)

var kickPendingLRPStartAuctionDuration = flag.Duration(
	"kickPendingLRPStartAuctionDuration",
	30*time.Second,
	"the interval between kicks to pending start auctions for long-running process",
)

var expireClaimedLRPStartAuctionDuration = flag.Duration(
	"expireClaimedLRPStartAuctionDuration",
	300*time.Second,
	"unclaimed start auctions for long-running processes are deleted, after this interval",
)

func main() {
	flag.Parse()

	logger := cf_lager.New("converger")

	bbs := initializeBBS(logger)

	cf_debug_server.Run()

	uuid, err := uuid.NewV4()
	if err != nil {
		logger.Fatal("Couldn't generate uuid", err)
	}

	heartbeater := bbs.NewConvergeLock(uuid.String(), *heartbeatInterval)

	converger := converger_process.New(
		bbs,
		logger,
		*convergeRepeatInterval,
		*kickPendingTaskDuration,
		*expirePendingTaskDuration,
		*expireCompletedTaskDuration,
		*kickPendingLRPStartAuctionDuration,
		*expireClaimedLRPStartAuctionDuration,
	)

	watcher := lrpwatcher.New(bbs, lrpreprocessor.New(bbs), logger)

	group := grouper.NewOrdered(os.Interrupt, grouper.Members{
		{"heartbeater", heartbeater},
		{"converger", converger},
		{"watcher", watcher},
	})

	logger.Info("started-waiting-for-lock")

	process := ifrit.Invoke(sigmon.New(group))

	logger.Info("acquired-lock")

	err = <-process.Wait()
	if err != nil {
		logger.Error("exited-with-failure", err)
		os.Exit(1)
	}

	logger.Info("exited")
}

func initializeBBS(logger lager.Logger) Bbs.ConvergerBBS {
	etcdAdapter := etcdstoreadapter.NewETCDStoreAdapter(
		strings.Split(*etcdCluster, ","),
		workerpool.NewWorkerPool(10),
	)

	err := etcdAdapter.Connect()
	if err != nil {
		logger.Fatal("failed-to-connect-to-etcd", err)
	}

	return Bbs.NewConvergerBBS(etcdAdapter, timeprovider.NewTimeProvider(), logger)
}
