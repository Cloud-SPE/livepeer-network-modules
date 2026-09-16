package desiredstate

import (
	"fmt"
	"sort"
	"strings"

	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/templates"
	"github.com/Cloud-SPE/livepeer-network-modules/pool-controller/internal/types"
)

func sortedCacheKeys(caches map[string]string) []string {
	var keys []string
	for key := range caches {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func cacheVolumes(name string, tmpl templates.Template) []string {
	keys := map[string]string{}
	for key := range tmpl.RunnerCompose.Caches {
		keys[key] = ""
	}
	for _, c := range tmpl.RunnerCompose.Companions {
		for key := range c.Caches {
			keys[key] = ""
		}
	}
	var out []string
	for _, key := range sortedCacheKeys(keys) {
		out = append(out, name+"-cache-"+key)
	}
	return out
}

func serviceRefs(value, name string, companions []templates.RunnerCompanion) string {
	value = strings.ReplaceAll(value, "{{service.main}}", name)
	for _, c := range companions {
		value = strings.ReplaceAll(value, "{{service."+c.Name+"}}", name+"-"+c.Name)
	}
	return strings.ReplaceAll(value, "$", "$$")
}

// A group is one desired service: only its primary endpoint attaches. Its
// companions and named caches follow exactly the same drain/stop lifecycle.
func renderCompose(name string, tmpl templates.Template, unit types.HardwareUnit, admission *HostAdmission) string {
	var out strings.Builder
	out.WriteString(renderContainer(name, tmpl, unit, admission, name))
	companions := append([]templates.RunnerCompanion(nil), tmpl.RunnerCompose.Companions...)
	sort.Slice(companions, func(i, j int) bool { return companions[i].Name < companions[j].Name })
	if len(companions) > 0 {
		out.WriteString("    depends_on:\n")
		for _, c := range companions {
			fmt.Fprintf(&out, "      - %s-%s\n", name, c.Name)
		}
	}
	for _, c := range companions {
		copy := tmpl
		gpu := c.GPU
		copy.RunnerCompose = templates.RunnerCompose{ShmSizeBytes: c.ShmSizeBytes, Image: c.Image, Command: c.Command, Env: c.Env, GPU: &gpu, Caches: c.Caches, Companions: companions}
		// Host-admission locks apply to every execution container in the group.
		out.WriteString(renderContainer(name+"-"+c.Name, copy, unit, admission, name))
	}
	return out.String()
}
