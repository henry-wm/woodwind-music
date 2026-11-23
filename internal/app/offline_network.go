package app

import "github.com/nsgo-dev/nsgo/core"

func (a *app) configureOfflineNetwork() {
	if a.disk == nil {
		return
	}
	if a.cx.Env().OS() == core.OSAndroid {
		a.disk.Network(!a.set.OfflineMobile, a.offlineUnmetered.Load)
	} else {
		a.disk.Network(false, nil)
	}
}
func (a *app) setupOfflineNetwork() {
	if a.disk == nil {
		return
	}
	if a.cx.Env().OS() == core.OSAndroid {
		update := func(network core.Network) {
			a.offlineUnmetered.Store(network.Online && !network.Expensive)
		}
		update(a.cx.Env().Network())
		a.cx.OnNetworkChange(func(network core.Network) {
			update(network)
			if network.Online {
				go a.disk.ResumeWaiting()
			}
		})
	}
	a.configureOfflineNetwork()
	go a.disk.ResumeAfterRestart()
}
