package core

import (
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	kg "github.com/accuknox/KubeArmor/KubeArmor/log"
	tp "github.com/accuknox/KubeArmor/KubeArmor/types"

	adt "github.com/accuknox/KubeArmor/KubeArmor/audit"
	efc "github.com/accuknox/KubeArmor/KubeArmor/enforcer"
	mon "github.com/accuknox/KubeArmor/KubeArmor/monitor"
)

// ====================== //
// == KubeArmor Daemon == //
// ====================== //

// StopChan Channel
var StopChan chan struct{}

// WgDaemon Handler
var WgDaemon sync.WaitGroup

// ActivePidMap to map container id and process id
var ActivePidMap map[string]tp.PidMap

// ActivePidMapLock for ActivePidMap
var ActivePidMapLock *sync.Mutex

// init Function
func init() {
	StopChan = make(chan struct{})
	WgDaemon = sync.WaitGroup{}

	// shared map between container monitor and audit logger
	ActivePidMap = map[string]tp.PidMap{}
	ActivePidMapLock = &sync.Mutex{}
}

// KubeArmorDaemon Structure
type KubeArmorDaemon struct {
	// home directory
	HomeDir string

	// containers (from docker)
	Containers     map[string]tp.Container
	ContainersLock *sync.Mutex

	// container groups
	ContainerGroups     []tp.ContainerGroup
	ContainerGroupsLock *sync.Mutex

	// K8s pods
	K8sPods     []tp.K8sPod
	K8sPodsLock *sync.Mutex

	// Security policies
	SecurityPolicies     []tp.SecurityPolicy
	SecurityPoliciesLock *sync.Mutex

	// runtime enforcer
	RuntimeEnforcer *efc.RuntimeEnforcer

	// audit logger
	AuditLogger *adt.AuditLogger

	// container monitor
	ContainerMonitor *mon.ContainerMonitor

	// logging
	AuditLogOption  string
	SystemLogOption string
}

// NewKubeArmorDaemon Function
func NewKubeArmorDaemon(auditLogOption, systemLogOption string) *KubeArmorDaemon {
	dm := new(KubeArmorDaemon)

	dir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		panic(err)
	}
	dm.HomeDir = dir

	dm.Containers = map[string]tp.Container{}
	dm.ContainersLock = &sync.Mutex{}

	dm.ContainerGroups = []tp.ContainerGroup{}
	dm.ContainerGroupsLock = &sync.Mutex{}

	dm.K8sPods = []tp.K8sPod{}
	dm.K8sPodsLock = &sync.Mutex{}

	dm.SecurityPolicies = []tp.SecurityPolicy{}
	dm.SecurityPoliciesLock = &sync.Mutex{}

	dm.RuntimeEnforcer = nil
	dm.AuditLogger = nil
	dm.ContainerMonitor = nil

	dm.AuditLogOption = auditLogOption
	dm.SystemLogOption = systemLogOption

	return dm
}

// DestroyKubeArmorDaemon Function
func (dm *KubeArmorDaemon) DestroyKubeArmorDaemon() {
	// close runtime enforcer
	dm.CloseRuntimeEnforcer()
	kg.PrintfNotInsert("Closed the runtime enforcer")

	// close audit logger
	dm.CloseAuditLogger()
	kg.PrintfNotInsert("Closed the audit logger")

	// close container monitor
	dm.CloseContainerMonitor()
	kg.PrintfNotInsert("Closed the container monitor")

	// wait for other routines
	kg.PrintfNotInsert("Waiting for routine terminations")
	WgDaemon.Wait()

	kg.PrintfNotInsert("Terminated the KubeArmor")
}

// ==================== //
// == Signal Handler == //
// ==================== //

// GetOSSigChannel Function
func GetOSSigChannel() chan os.Signal {
	c := make(chan os.Signal, 1)

	signal.Notify(c,
		syscall.SIGKILL,
		syscall.SIGHUP,
		syscall.SIGINT,
		syscall.SIGTERM,
		syscall.SIGQUIT,
		os.Interrupt)

	return c
}

// GetChan Function
func (dm *KubeArmorDaemon) GetChan() chan os.Signal {
	sigChan := GetOSSigChannel()

	select {
	case <-sigChan:
		kg.PrintfNotInsert("Got a signal to terminate the KubeArmor")
		close(StopChan)

		dm.DestroyKubeArmorDaemon()

		os.Exit(0)
	default:
		time.Sleep(time.Second * 1)
	}

	return sigChan
}

// ====================== //
// == Runtime Enforcer == //
// ====================== //

// InitRuntimeEnforcer Function
func (dm *KubeArmorDaemon) InitRuntimeEnforcer() bool {
	ret := true
	defer kg.HandleErrRet(&ret)

	dm.RuntimeEnforcer = efc.NewRuntimeEnforcer(dm.HomeDir)

	kg.Print("Started to protect containers")

	return ret
}

// CloseRuntimeEnforcer Function
func (dm *KubeArmorDaemon) CloseRuntimeEnforcer() {
	dm.RuntimeEnforcer.DestroyRuntimeEnforcer()
}

// ================== //
// == Audit Logger == //
// ================== //

// InitAuditLogger Function
func (dm *KubeArmorDaemon) InitAuditLogger() bool {
	ret := true
	defer kg.HandleErrRet(&ret)

	dm.AuditLogger = adt.NewAuditLogger(dm.AuditLogOption, dm.Containers, dm.ContainersLock, ActivePidMap, ActivePidMapLock)
	if err := dm.AuditLogger.InitAuditLogger(dm.HomeDir); err != nil {
		return false
	}

	kg.Print("Started to monitor audit logs")

	return ret
}

// MonitorAuditLogs Function
func (dm *KubeArmorDaemon) MonitorAuditLogs() {
	defer kg.HandleErr()
	defer WgDaemon.Done()

	go dm.AuditLogger.MonitorAuditLogs()
}

// CloseAuditLogger Function
func (dm *KubeArmorDaemon) CloseAuditLogger() {
	dm.AuditLogger.DestroyAuditLogger()
}

// ======================= //
// == Container Monitor == //
// ======================= //

// InitContainerMonitor Function
func (dm *KubeArmorDaemon) InitContainerMonitor() bool {
	ret := true
	defer kg.HandleErrRet(&ret)

	dm.ContainerMonitor = mon.NewContainerMonitor(dm.SystemLogOption, dm.Containers, dm.ContainersLock, ActivePidMap, ActivePidMapLock)
	if err := dm.ContainerMonitor.InitBPF(dm.HomeDir); err != nil {
		return false
	}

	kg.Print("Started to monitor system events")

	return ret
}

// MonitorSystemEvents Function
func (dm *KubeArmorDaemon) MonitorSystemEvents() {
	defer kg.HandleErr()
	defer WgDaemon.Done()

	go dm.ContainerMonitor.TraceSyscall()
	go dm.ContainerMonitor.TraceSkb()

	go dm.ContainerMonitor.UpdateSystemLogs()
}

// CloseContainerMonitor Function
func (dm *KubeArmorDaemon) CloseContainerMonitor() {
	dm.ContainerMonitor.DestroyContainerMonitor()
}

// ========== //
// == Main == //
// ========== //

// KubeArmor Function
func KubeArmor(auditLogOption, systemLogOption string) {
	dm := NewKubeArmorDaemon(auditLogOption, systemLogOption)

	kg.Print("Started KubeArmor")

	// initialize runtime enforcer
	if !dm.InitRuntimeEnforcer() {
		kg.Err("Failed to intialize the runtime enforcer")
		return
	}

	// initialize audit logger
	if !dm.InitAuditLogger() {
		kg.Err("Failed to intialize the audit logger")
		return
	}

	// initialize container monitor
	if !dm.InitContainerMonitor() {
		kg.Err("Failed to initialize the container monitor")
		return
	}

	// monitor audit logs (audit logger)
	go dm.MonitorAuditLogs()
	WgDaemon.Add(1)

	// monior system events (container monitor)
	go dm.MonitorSystemEvents()
	WgDaemon.Add(1)

	// wait for a while
	time.Sleep(time.Second * 1)

	// == //

	if K8s.InitK8sClient() {
		// get current CRI
		cr := K8s.GetContainerRuntime()

		kg.Printf("Container Runtime: %s", cr)

		if strings.Contains(cr, "containerd") {
			// monitor containerd events
			go dm.MonitorContainerdEvents()
			WgDaemon.Add(1)
		} else if strings.Contains(cr, "docker") {
			// monitor docker events
			go dm.MonitorDockerEvents()
			WgDaemon.Add(1)
		}

		// watch k8s pods
		go dm.WatchK8sPods()

		// watch security policies
		go dm.WatchSecurityPolicies()
	}

	// listen for interrupt signals
	sigChan := dm.GetChan()
	<-sigChan
	kg.PrintfNotInsert("Got a signal to terminate the KubeArmor")
	close(StopChan)

	dm.DestroyKubeArmorDaemon()
}
