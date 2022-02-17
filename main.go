package main

import (
	"fmt"
	"os"
	"strconv"

	log "github.com/sirupsen/logrus"
	cni "github.com/travishegner/go-libcni"
	"github.com/travishegner/routetable-ipam/address"
)

func main() {
	var exitOutput []byte
	exitCode := 0
	lf, err := os.OpenFile("/var/log/route-table.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		exitCode, exitOutput = cni.PrepareExit(err, 99, "failed to open log file")
		return
	}
	defer lf.Close()
	log.SetOutput(lf)
	log.SetLevel(log.DebugLevel)

	defer func() {
		r := recover()
		if r != nil {
			err, ok := r.(error)
			if !ok {
				err = fmt.Errorf("panic: %v", r)
			}
			exitCode, exitOutput = cni.PrepareExit(err, 99, "panic during execution")
		}
		exit(exitCode, exitOutput)
	}()

	log.WithField("command", os.Getenv("CNI_COMMAND")).Debug()
	varNames := []string{"CNI_COMMAND", "CNI_CONTAINERID", "CNI_NETNS", "CNI_IFNAME", "CNI_ARGS", "CNI_PATH"}
	varMap := log.Fields{}
	for _, vn := range varNames {
		varMap[vn] = os.Getenv(vn)
	}
	log.WithFields(varMap).Debug("vars")

	//Read CNI standard environment variables
	vars := cni.NewVars()

	if vars.Command == "VERSION" {
		//report supported cni versions
		exitOutput = []byte(fmt.Sprintf("{\"cniVersion\": \"%v\", \"supportedVersions\": [\"%v\"]}", cni.CNIVersion, cni.CNIVersion))
		return
	}

	cidr, ok := vars.GetArg("CIDR")
	if !ok {
		exitCode, exitOutput = cni.PrepareExit(fmt.Errorf("CNI_ARGS must contain CIDR=<cidr>, where <cidr> represents the address/subnet from which to choose an address"), 7, "missing cidr arg")
		return
	}

	xf := 0
	sxf, ok := vars.GetArg("EXCLUDE_FIRST")
	if ok {
		xf, err = strconv.Atoi(sxf)
		if err != nil {
			exitCode, exitOutput = cni.PrepareExit(err, 11, "couldn't parse EXCLUDE_FIRST")
			return
		}
	}

	xl := 0
	sxl, ok := vars.GetArg("EXCLUDE_LAST")
	if ok {
		xl, err = strconv.Atoi(sxl)
		if err != nil {
			exitCode, exitOutput = cni.PrepareExit(err, 11, "couldn't parse EXCLUDE_LAST")
			return
		}
	}

	li := -1
	sli, ok := vars.GetArg("LINK_INDEX")
	if ok {
		li, err = strconv.Atoi(sli)
		if err != nil {
			exitCode, exitOutput = cni.PrepareExit(err, 11, "couldn't parse LINK_INDEX")
			return
		}
	}

	switch vars.Command {
	case "ADD":
		result, err := handleAdd(cidr, li, xf, xl)
		if err != nil {
			log.WithError(err).Error("error while handling add")
			exitCode, exitOutput = cni.PrepareExit(err, 11, "failed while adding address")
			return
		}

		os.Stdout.Write(result)
	case "DEL":
		err := handleDel(cidr, li)
		if err != nil {
			log.WithError(err).Error("error while handling del")
			exitCode, exitOutput = cni.PrepareExit(err, 11, "failed while deleting address")
			return
		}
	case "CHECK":
		err := handleCheck(cidr, li)
		if err != nil {
			log.WithError(err).Error("error while handling check")
			exitCode, exitOutput = cni.PrepareExit(err, 11, "failed while checking address")
			return
		}
	default:
		log.Error("invalid CNI command")
		exitCode, exitOutput = cni.PrepareExit(nil, 4, "invalid CNI_COMMAND")
		return
	}
}

func exit(code int, output []byte) {
	os.Stdout.Write(output)
	os.Exit(code)
}

func handleAdd(cidr string, linkIndex, excludeFirst, excludeLast int) ([]byte, error) {
	log.Debugf("handleAdd(%v, %v, %v, %v)", cidr, linkIndex, excludeFirst, excludeLast)

	addr, err := address.New(cidr, linkIndex, excludeFirst, excludeLast)
	if err != nil {
		return nil, fmt.Errorf("failed to create new address %v: %w", cidr, err)
	}
	ipVer := "4"
	if addr.IPNet.IP.To4() == nil {
		ipVer = "6"
	}
	ips := make([]*cni.IP, 1)
	ips[0] = &cni.IP{
		Version: ipVer,
		Address: addr.IPNet.String(),
	}
	result := &cni.Result{
		CNIVersion: cni.CNIVersion,
		IPs:        ips,
	}

	return result.Marshal(), nil
}

func handleDel(cidr string, linkIndex int) error {
	log.Debugf("handleDel(%v, %v)", cidr, linkIndex)

	addr, err := address.Get(cidr, linkIndex)
	if err != nil {
		return fmt.Errorf("failed to get address %v: %w", cidr, err)
	}

	err = addr.Delete()
	if err != nil {
		return fmt.Errorf("failed to delete address %v: %w", cidr, err)
	}

	return nil
}

func handleCheck(cidr string, linkIndex int) error {
	log.Debugf("handleCheck(%v, %v)", cidr, linkIndex)
	return nil
}
