package main

func initFormFeatures() {
	formFeatures.SetTitle("Select Features").SetBorder(true)

	var newVars map[string]interface{}

	formFeatures.AddCheckbox("Open firewall rules on each node", inventory.All.Vars["set_firewall_rules"].(bool), func(checked bool) {
		if checked {
			newVars["set_firewall_rules"] = true
		} else {
			newVars["set_firewall_rules"] = false
		}
	})
}
