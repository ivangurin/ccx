package handlers

import (
	"net/http"
)

func handleAdminHealth(d Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		provider, protocol, active := d.Router.Active()
		resp := map[string]any{
			"version": d.Version,
			// /health answering already implies the route is ready (Build ran
			// before listening); kept as a constant for probe compatibility.
			"ready":  true,
			"port":   d.Port,
			"active": nil,
		}
		if active {
			resp["active"] = map[string]string{"provider": provider, "protocol": string(protocol)}
		}
		writeJSON(w, resp)
	}
}
