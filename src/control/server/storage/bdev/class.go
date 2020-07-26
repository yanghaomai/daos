//
// (C) Copyright 2018-2020 Intel Corporation.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//    http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
//
// GOVERNMENT LICENSE RIGHTS-OPEN SOURCE SOFTWARE
// The Government's rights to use, modify, reproduce, release, perform, display,
// or disclose this software are subject to the terms of the Apache License as
// provided in Contract No. 8F-30005.
// Any reproduction of computer software, computer software documentation, or
// portions thereof marked with this legend must also reproduce the markings.
//

package bdev

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"text/template"

	"github.com/pkg/errors"

	"github.com/daos-stack/daos/src/control/common"
	"github.com/daos-stack/daos/src/control/logging"
	"github.com/daos-stack/daos/src/control/server/storage"
)

const (
	confOut   = "daos_nvme.conf"
	nvmeTempl = `[Nvme]
{{ $host := .Hostname }}{{ range $i, $e := .DeviceList }}    TransportID "trtype:PCIe traddr:{{$e}}" Nvme_{{$host}}_{{$i}}
{{ end }}    RetryCount 4
    TimeoutUsec 0
    ActionOnTimeout None
    AdminPollRate 100000
    HotplugEnable No
    HotplugPollRate 0
`
	// device block size hardcoded to 4096
	fileTempl = `[AIO]
{{ $host := .Hostname }}{{ range $i, $e := .DeviceList }}    AIO {{$e}} AIO_{{$host}}_{{$i}} 4096
{{ end }}`
	kdevTempl = `[AIO]
{{ $host := .Hostname }}{{ range $i, $e := .DeviceList }}    AIO {{$e}} AIO_{{$host}}_{{$i}}
{{ end }}`
	mallocTempl = `[Malloc]
    NumberOfLuns {{.DeviceCount}}
    LunSizeInMB {{.FileSize}}000
`
	gbyte   = 1000000000
	blkSize = 4096

	msgBdevNone    = "in config, no nvme.conf generated for server"
	msgBdevEmpty   = "bdev device list entry empty"
	msgBdevBadSize = "backfile_size should be greater than 0"
)

// bdev describes parameters and behaviours for a particular bdev class.
type bdev struct {
	templ   string
	vosEnv  string
	isEmpty func(*storage.BdevConfig) string                // check no elements
	isValid func(*storage.BdevConfig) string                // check valid elements
	init    func(logging.Logger, *storage.BdevConfig) error // prerequisite actions
}

func nilValidate(_ *storage.BdevConfig) string { return "" }

func nilInit(_ logging.Logger, _ *storage.BdevConfig) error { return nil }

func isEmptyList(c *storage.BdevConfig) string {
	if len(c.DeviceList) == 0 {
		return "bdev_list empty " + msgBdevNone
	}

	return ""
}

func isEmptyNumber(c *storage.BdevConfig) string {
	if c.DeviceCount == 0 {
		return "bdev_number == 0 " + msgBdevNone
	}

	return ""
}

func isValidList(c *storage.BdevConfig) string {
	for i, elem := range c.DeviceList {
		if elem == "" {
			return fmt.Sprintf("%s (index %d)", msgBdevEmpty, i)
		}
	}

	return ""
}

func isValidSize(c *storage.BdevConfig) string {
	if c.FileSize < 1 {
		return msgBdevBadSize
	}

	return ""
}

func createEmptyFile(log logging.Logger, path string, size int64) error {
	if !filepath.IsAbs(path) {
		return errors.Errorf("please specify absolute path (%s)", path)
	}

	if _, err := os.Stat(path); !os.IsNotExist(err) {
		return err
	}

	file, err := common.TruncFile(path)
	if err != nil {
		return err
	}
	defer file.Close()

	if err := syscall.Fallocate(int(file.Fd()), 0, 0, size); err != nil {
		e, ok := err.(syscall.Errno)
		if ok && (e == syscall.ENOSYS || e == syscall.EOPNOTSUPP) {
			log.Debugf(
				"Warning: Fallocate not supported, attempting Truncate: ", e)

			if err := file.Truncate(size); err != nil {
				return err
			}
		}
	}

	return nil
}

func bdevFileInit(log logging.Logger, c *storage.BdevConfig) error {
	// truncate or create files for SPDK AIO emulation,
	// requested size aligned with block size
	size := (int64(c.FileSize*gbyte) / int64(blkSize)) * int64(blkSize)

	for _, path := range c.DeviceList {
		err := createEmptyFile(log, path, size)
		if err != nil {
			return err
		}
	}

	return nil
}

// ParsePciAddress returns separated components of BDF format PCI address.
func ParsePciAddress(addr string) (string, string, string, string, error) {
	parts := strings.Split(addr, ":")
	deviceFunc := strings.Split(parts[len(parts)-1], ".")
	if len(parts) != 3 || len(deviceFunc) != 2 {
		return "", "", "", "",
			errors.Errorf("unexpected pci address bdf format: %q", addr)
	}

	return parts[0], parts[1], deviceFunc[0], deviceFunc[1], nil
}

// bdevNvmeInit performs any necessary preparation forNVME class bdev config.
//
// Augment bdev device list if VMD backing SSD PCI addresses have been added to
// config.
func bdevNvmeInit(log logging.Logger, c *storage.BdevConfig) error {
	log.Debugf("init bdev nvme, vmds: %v", c.VmdDeviceList)
	if len(c.VmdDeviceList) == 0 {
		return nil
	}

	logMsg := fmt.Sprintf("VMD: prepare conf device list, before: %v, vmds: %v",
		c.VmdDeviceList, c.DeviceList)

	var newDevList []string
	// Remove VMD addrs from DeviceList and replace with NVMe addrs behind VMD
	for _, addr := range c.DeviceList {
		if !common.Includes(c.VmdDeviceList, addr) {
			newDevList = append(newDevList, addr)
			continue
		}

		// build the concatenated form of vmd bdf
		_, b, d, f, err := ParsePciAddress(addr)
		if err != nil {
			return err
		}
		prefix := fmt.Sprintf("%02s%02s%02s", b, d, f)

		log.Debugf("looking for prefix %s in vmd list %v", prefix, c.VmdDeviceList)
		// find backing ssds with matching concat vmd bdf in domain of pci addr
		for _, vmdDevAddr := range c.VmdDeviceList {
			domain, _, _, _, err := ParsePciAddress(vmdDevAddr)
			if err != nil {
				return err
			}
			if domain == prefix {
				log.Debugf("adding backing device %s", vmdDevAddr)
				newDevList = append(newDevList, vmdDevAddr)
				//strings.Replace(vmdDevAddr, prefix, "0000", 1))
				log.Debugf("new dev list: %v", newDevList)
			}
		}
	}

	c.DeviceList = newDevList

	log.Debug(fmt.Sprintf("%s, after: %v", logMsg, c.DeviceList))

	return nil
}

// genFromNvme takes NVMe device PCI addresses and generates config content
// (output as string) from template.
func genFromTempl(cfg *storage.BdevConfig, templ string) (out bytes.Buffer, err error) {
	fmt.Printf("generating from template")

	if len(cfg.VmdDeviceList) > 0 {
		fmt.Printf("got it")
		templ = `[VMD]
    Enable True

` + templ
	}

	t := template.Must(template.New(confOut).Parse(templ))
	err = t.Execute(&out, cfg)

	return
}

// ClassProvider implements functionality for a given bdev class
type ClassProvider struct {
	log     logging.Logger
	cfg     *storage.BdevConfig
	cfgPath string
	bdev    bdev
}

// NewClassProvider returns a new ClassProvider reference for given bdev type.
func NewClassProvider(log logging.Logger, cfgDir string, cfg *storage.BdevConfig) (*ClassProvider, error) {
	p := &ClassProvider{
		log: log,
		cfg: cfg,
	}

	log.Debug("creating new class provider")
	switch cfg.Class {
	case storage.BdevClassNone:
		p.bdev = bdev{nvmeTempl, "", isEmptyList, isValidList, nilInit}
	case storage.BdevClassNvme:
		log.Debug("bdev nvme detected")
		p.bdev = bdev{nvmeTempl, "NVME", isEmptyList, isValidList, bdevNvmeInit}
		if len(cfg.VmdDeviceList) > 0 {
			log.Debug("set vmd vos env")
			p.bdev.vosEnv = "VMD"
		}
	case storage.BdevClassMalloc:
		p.bdev = bdev{mallocTempl, "MALLOC", isEmptyNumber, nilValidate, nilInit}
	case storage.BdevClassKdev:
		p.bdev = bdev{kdevTempl, "AIO", isEmptyList, isValidList, nilInit}
	case storage.BdevClassFile:
		p.bdev = bdev{fileTempl, "AIO", isEmptyList, isValidSize, bdevFileInit}
	default:
		return nil, errors.Errorf("unable to map %q to BdevClass", cfg.Class)
	}

	if msg := p.bdev.isEmpty(p.cfg); msg != "" {
		log.Debugf("spdk %s: %s", cfg.Class, msg)
		// No devices; no need to generate a config file
		return p, nil
	}

	if msg := p.bdev.isValid(p.cfg); msg != "" {
		log.Debugf("spdk %s: %s", cfg.Class, msg)
		// Bad config; don't generate a config file
		return nil, errors.Errorf("invalid NVMe config: %s", msg)
	}

	// Config file required; set this so it gets generated later
	p.cfgPath = filepath.Join(cfgDir, confOut)

	// FIXME: Not really happy with having side-effects here, but trying
	// not to change too much at once.
	cfg.VosEnv = p.bdev.vosEnv
	cfg.ConfigPath = p.cfgPath

	return p, nil
}

// GenConfigFile generates nvme config file for given bdev type to be consumed
// by spdk.
func (p *ClassProvider) GenConfigFile() error {
	if p.cfgPath == "" {
		return nil
	}

	if err := p.bdev.init(p.log, p.cfg); err != nil {
		return errors.Wrap(err, "bdev device init")
	}

	confBytes, err := genFromTempl(p.cfg, p.bdev.templ)
	if err != nil {
		return err
	}

	if confBytes.Len() == 0 {
		return errors.New("spdk: generated NVMe config is unexpectedly empty")
	}

	f, err := os.Create(p.cfgPath)
	defer func() {
		ce := f.Close()
		if err == nil {
			err = ce
		}
	}()
	if err != nil {
		return errors.Wrapf(err, "spdk: failed to create NVMe config file %s", p.cfgPath)
	}
	if _, err := confBytes.WriteTo(f); err != nil {
		return errors.Wrapf(err, "spdk: failed to write NVMe config to file %s", p.cfgPath)
	}

	return nil
}
