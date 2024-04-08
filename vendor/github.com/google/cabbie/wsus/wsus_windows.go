// Copyright 2019 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build windows
// +build windows

package wsus

import (
	"fmt"
	"net/http"
	"time"

	"github.com/google/cabbie/cablib"
	"golang.org/x/sys/windows/registry"
	"golang.org/x/sys/windows/svc/eventlog"
)

var (
	wlog *eventlog.Log
)

func responseTime(name string) time.Duration {
	req, err := http.NewRequest("GET", fmt.Sprintf("https://%s", name), nil)
	if err != nil {
		wlog.Warning(3, fmt.Sprintf("Failed to create new http request: %v", err))
		return 0
	}

	start := time.Now()
	resp, err := http.DefaultTransport.RoundTrip(req)
	if err != nil {
		wlog.Warning(3, fmt.Sprintf("Failed to send GET request to: %v", err))
		return 0
	}

	if resp.StatusCode != http.StatusOK {
		wlog.Warning(3, fmt.Sprintf("Non-200 status code returned(%d)", resp.StatusCode))
		return 0
	}

	return time.Since(start)
}

// Init will initialize the local update client with the desired WSUS config.
func Init(servers []string) (*WSUS, error) {
	var w WSUS
	var err error

	if len(servers) == 0 {
		w.ServerSelection = WindowsUpdate
		return &w, w.Clear()
	}

	wlog, err = eventlog.Open("Cabbie WSUS")
	if err != nil {
		return &w, err
	}

	w.order(servers)

	if len(w.Servers) == 0 {
		w.ServerSelection = WindowsUpdate
		return &w, w.Clear()
	}

	if err := w.Set(0); err != nil {
		w.ServerSelection = WindowsUpdate
		return &w, fmt.Errorf("error setting WSUS config:\n%v", err)
	}
	w.ServerSelection = ManagedServer
	return &w, nil
}

// order returns a list of WSUS servers from fastest to slowest.
func (w *WSUS) order(servers []string) {

	s := make(map[int]string, len(servers))
	for _, n := range servers {
		t := responseTime(n)
		if t == 0 {
			wlog.Warning(2, fmt.Sprintf("Skipping WSUS server %s as it appears to be unreachable", n))
			continue
		}
		s[int(t)] = n
	}
	k := sortedKeys(s)

	for _, key := range k {
		w.Servers = append(w.Servers, s[key])
	}
}

// Set configures the update client to use the requested WSUS server.
func (w *WSUS) Set(index int) error {
	k, err := registry.OpenKey(registry.LOCAL_MACHINE, cablib.WUReg, registry.ALL_ACCESS)
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	if err == registry.ErrNotExist {
		k, _, err = registry.CreateKey(registry.LOCAL_MACHINE, cablib.WUReg, registry.ALL_ACCESS)
		if err != nil {
			return err
		}
	}
	defer k.Close()

	if index > (len(w.Servers) - 1) {
		return fmt.Errorf("requested index (%d) is out of selectable server range (%d)", index, (len(w.Servers) - 1))
	}
	name := w.Servers[index]
	url := fmt.Sprintf("https://%s", name)
	if err := k.SetStringValue("WUServer", url); err != nil {
		return err
	}
	w.CurrentServer = name

	if err := k.SetStringValue("WUStatusServer", url); err != nil {
		return err
	}

	sk, _, err := registry.CreateKey(k, "AU", registry.ALL_ACCESS)
	if err != nil {
		return err
	}
	defer sk.Close()

	return sk.SetDWordValue("UseWUServer", 1)
}

// Clear sets WSUS client configurations back to Windows defaults.
func (w *WSUS) Clear() error {
	w.CurrentServer = ""
	w.ServerSelection = WindowsUpdate

	k, err := registry.OpenKey(registry.LOCAL_MACHINE, cablib.WUReg, registry.ALL_ACCESS)
	if err != nil && err != registry.ErrNotExist {
		return err
	}
	if err == registry.ErrNotExist {
		return nil
	}
	defer k.Close()

	err = k.DeleteValue("WUServer")
	if err != nil && err != registry.ErrNotExist {
		wlog.Warning(4, fmt.Sprintf("Failed to delete WUServer registry value: %v", err))
	}
	err = k.DeleteValue("WUStatusServer")
	if err != nil && err != registry.ErrNotExist {
		wlog.Warning(4, fmt.Sprintf("Failed to delete WUStatusServer registry value: %v", err))
	}

	auk, err := registry.OpenKey(k, "AU", registry.ALL_ACCESS)
	if err == registry.ErrNotExist {
		return nil
	} else if err != nil {
		return err
	}

	defer auk.Close()

	err = auk.DeleteValue("UseWUServer")
	if err != nil && err != registry.ErrNotExist {
		return fmt.Errorf("Failed to delete UseWUServer registry value: %v", err)
	}
	return nil
}
