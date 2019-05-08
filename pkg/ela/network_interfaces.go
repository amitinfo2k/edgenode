// Copyright 2019 Intel Corporation and Smart-Edge.com, Inc. All rights reserved
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package ela

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"github.com/kata-containers/runtime/virtcontainers/pkg/nsenter"
	"io/ioutil"
	"net"
	"os"
	"os/exec"
	"path"
	"regexp"
	"strings"

	"github.com/pkg/errors"
	"github.com/smartedgemec/appliance-ce/pkg/ela/ini"
)

type NetworkDevice struct {
	PCI          string
	Manufacturer string
	Name         string
	MAC          string
	Description  string
}

func getNetworkPCIs() ([]NetworkDevice, error) {
	cmd := exec.Command("bash", "-c",
		`lspci -Dmm | grep -i "Ethernet\|Network"`)
	var out bytes.Buffer
	cmd.Stdout = &out

	err := cmd.Run()
	if err != nil {
		return nil, errors.Errorf("Failed to exec lspci command: %s",
			err.Error())
	}

	csvReader := csv.NewReader(strings.NewReader(out.String()))
	csvReader.Comma = ' '

	records, err := csvReader.ReadAll()
	if err != nil {
		return nil, errors.Errorf("Failed to parse CSV because: %v. Input: %s",
			err.Error(), out.String())
	}

	devs := make([]NetworkDevice, 0)

	for _, rec := range records {
		if len(rec) >= 4 {
			pci, manufacturer, devName := rec[0], rec[2], rec[3]

			devs = append(devs, NetworkDevice{
				PCI:          pci,
				Manufacturer: manufacturer,
				Name:         devName,
			})
		}
	}

	return devs, nil
}

func fillMACAddrForKernelDevs(devs []NetworkDevice) error {
	var ifs []net.Interface
	var ifsErr error

	getIfs := func() error {
		ifs, ifsErr = net.Interfaces()
		return ifsErr
	}

	ns := []nsenter.Namespace{
		{Path: "/var/host_ns/net", Type: nsenter.NSTypeNet}}
	err := nsenter.NsEnter(ns, getIfs)

	if err != nil {
		return errors.Wrap(err, "failed to enter namespace")
	}

	if ifsErr != nil {
		return errors.Wrap(ifsErr, "failed to obtain interfaces")
	}

	pciRegexp := regexp.MustCompile(
		`([0-9]{0,4}:[0-9a-f]{2}:[0-9a-f]{2}\.[0-9a-f]{1})`)

	for _, iface := range ifs {
		ueventPath := path.Join("/var/host_net_devices", iface.Name,
			"device/uevent")
		content, err := ioutil.ReadFile(ueventPath)
		if err != nil {
			if os.IsNotExist(err) {
				// "File not found" is expected
				continue
			}

			return errors.Wrapf(err, "Failed to load uevent file: %s",
				ueventPath)
		}

		pci := pciRegexp.FindString(string(content))

		for idx := range devs {
			if devs[idx].PCI == pci {
				devs[idx].MAC = iface.HardwareAddr.String()
				devs[idx].Name = fmt.Sprintf("[%s] %s", iface.Name,
					devs[idx].Name)
			}
		}
	}

	return nil
}

func fillMACAddrForDPDKDevs(devs []NetworkDevice) error {
	ntsCfg, err := ini.NtsConfigFromFile(Config.NtsConfigPath)

	if err != nil {
		return errors.Wrap(err, "failed to read NTS config")
	}

	for _, port := range ntsCfg.Ports {
		for idx := range devs {
			if devs[idx].PCI == port.PciAddress {
				devs[idx].MAC = port.MAC
				devs[idx].Name = port.Name
				devs[idx].Description = port.Description
			}
		}
	}

	return nil
}

func GetNetworkDevices() ([]NetworkDevice, error) {
	devs, err := getNetworkPCIs()
	if err != nil {
		return nil, err
	}

	err = fillMACAddrForDPDKDevs(devs)
	if err != nil {
		return nil, err
	}

	err = fillMACAddrForKernelDevs(devs)
	if err != nil {
		return nil, err
	}

	return devs, err
}