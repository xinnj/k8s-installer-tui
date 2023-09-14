package main

import (
	"github.com/rivo/tview"
	"gopkg.in/yaml.v3"
	"strconv"
)

func initFlexFeatures() {
	formFeatures := tview.NewForm()
	formFeatures.SetTitle("Set Features").SetBorder(true)

	newVars := make(map[string]any)
	mapYaml := make(map[string]string)

	for _, oneVar := range appConfig.Configuable_vars {
		var key string
		var value any
		for key, value = range oneVar["var"].(map[string]any) {
			break
		}

		// Load value from inventory if exists
		if inventory.All.Vars[key] != nil {
			value = inventory.All.Vars[key]
		}

		newVars[key] = value

		switch value.(type) {
		case bool:
			formFeatures.AddCheckbox(oneVar["description"].(string), value.(bool), func(checked bool) {
				if checked {
					newVars[key] = true
				} else {
					newVars[key] = false
				}
			})
		case map[string]any:
			valueByte, err := yaml.Marshal(&value)
			check(err)
			valueString := string(valueByte)
			formFeatures.AddTextArea(oneVar["description"].(string), valueString, 0, 0, 0, func(text string) {
				mapYaml[key] = text
			})
		case string:
			formFeatures.AddInputField(oneVar["description"].(string), value.(string), 0, nil, func(text string) {
				newVars[key] = text
			})
		case int:
			valueString := strconv.Itoa(value.(int))
			formFeatures.AddInputField(oneVar["description"].(string), valueString, 0, tview.InputFieldInteger, func(text string) {
				i, err := strconv.Atoi(text)
				if err == nil {
					newVars[key] = i
				}
			})
		default:
			panic("Can't recognize configurable var: " + key)
		}
	}

	formDown := tview.NewForm()

	formDown.AddButton("Save & Next", func() {
		for key, value := range mapYaml {
			valueMap := make(map[string]any)
			err := yaml.Unmarshal([]byte(value), valueMap)
			if err != nil {
				showErrorModal(key+" has wrong format.",
					func(buttonIndex int, buttonLabel string) {
						pages.SwitchToPage("Features")
					})
			} else {
				newVars[key] = valueMap
			}
		}

		for key, value := range newVars {
			inventory.All.Vars[key] = value
		}

		saveInventory()
		flexHaMode.Clear()
		initFlexHaMode()
		pages.SwitchToPage("HA Mode")
	})

	formDown.AddButton("Cancel", func() {
		pages.SwitchToPage("Edit Hosts")
	})

	flexFeatures.SetDirection(tview.FlexRow).
		AddItem(formFeatures, 0, 1, true).
		AddItem(formDown, 3, 1, false)
}
