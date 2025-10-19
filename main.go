package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/pflag"
	"golang.org/x/term"
	"sigs.k8s.io/yaml"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	_ "k8s.io/client-go/plugin/pkg/client/auth"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/util/homedir"
)

// Velero PodVolumeBackup GVR
var podVolumeBackupGVR = schema.GroupVersionResource{
	Group:    "velero.io",
	Version:  "v1",
	Resource: "podvolumebackups",
}

type row struct {
	PodName        string `json:"podName" yaml:"podName"`
	PodNamespace   string `json:"podNamespace" yaml:"podNamespace"`
	Volume         string `json:"volume" yaml:"volume"`
	SizeBytes      *int64 `json:"sizeBytes,omitempty" yaml:"sizeBytes,omitempty"`
	SizeHuman      string `json:"sizeHuman,omitempty" yaml:"sizeHuman,omitempty"`
	Created        string `json:"created,omitempty" yaml:"created,omitempty"`
	CreatedRFC3339 string `json:"createdRFC3339,omitempty" yaml:"createdRFC3339,omitempty"`
	BackupName     string `json:"backupName,omitempty" yaml:"backupName,omitempty"`
	ResourceName   string `json:"resourceName,omitempty" yaml:"resourceName,omitempty"`
}

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %ciB", float64(b)/float64(div), "KMGTPE"[exp])
}

func getInt64(v interface{}) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int32:
		return int64(t), true
	case int:
		return int64(t), true
	case float64:
		return int64(t), true
	case json.Number:
		if n, err := t.Int64(); err == nil {
			return n, true
		}
	case string:
		if n, err := strconv.ParseInt(t, 10, 64); err == nil {
			return n, true
		}
	}
	return 0, false
}

func splitCSV(s string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

func anyContainsFold(hay string, needles []string) bool {
	if len(needles) == 0 {
		return true
	}
	h := strings.ToLower(hay)
	for _, n := range needles {
		if strings.Contains(h, strings.ToLower(n)) {
			return true
		}
	}
	return false
}

func anyEqual(hay string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, a := range allowed {
		if hay == a {
			return true
		}
	}
	return false
}

// color helpers
func detectColor(mode string) bool {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "never" {
		return false
	}
	if mode == "always" {
		return true
	}
	// auto
	if os.Getenv("NO_COLOR") != "" {
		return false
	}
	return term.IsTerminal(int(os.Stdout.Fd()))
}
func c(enabled bool, code string, s string) string {
	if !enabled || s == "" {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}
func prettyDivider(enabled bool) string {
	line := strings.Repeat("─", 72)
	return c(enabled, "2", line) // dim
}

func main() {
	// Flaggor
	podFilterFlag := pflag.String("pod", "", "Comma-separated list. Include items where pod name contains ANY of these substrings (case-insensitive)")
	podNSFilterFlag := pflag.String("pod-namespace", "", "Comma-separated list. Include items where pod namespace equals ANY of these (exact match)")
	volumeFilterFlag := pflag.String("volume", "", "Comma-separated list. Include items where volume equals ANY of these (exact match)")
	allFlag := pflag.Bool("all", false, "List all podvolumebackups instead of filtering by backup name prefix")
	veleroNsFlag := pflag.String("velero-namespace", "velero", "Namespace where PodVolumeBackup CRs are (default: velero)")
	outputFlag := pflag.StringP("output", "o", "table", "Output format: table|json|yaml|csv|pretty")
	colorFlag := pflag.String("color", "auto", "Color mode for pretty output: auto|always|never")
	debugFlag := pflag.Bool("debug", false, "Print debug info to stderr")

	pflag.Usage = func() {
		fmt.Fprintf(os.Stderr, `
Usage:
  kubectl podvolumebackup-list [prefix|--all] [--velero-namespace=<ns>] [--pod=a,b] [--pod-namespace=x,y] [--volume=v1,v2] [-o table|json|yaml|csv|pretty] [--color=auto|always|never] [--debug]

Notes:
  --pod            substring match (case-insensitive), ANY of comma-separated values
  --pod-namespace  exact match, ANY of comma-separated values
  --volume         exact match, ANY of comma-separated values

Examples:
  kubectl podvolumebackup-list --all --pod=nginx,redis --pod-namespace=dev,prod --volume=data,cache -o pretty
  kubectl podvolumebackup-list nightly- --pod=nginx --pod-namespace=prod --volume=myvol --velero-namespace=velero -o json
`)
		pflag.PrintDefaults()
	}
	pflag.Parse()

	// Hantera prefix (positionsargument) eller --all
	var prefix string
	if !*allFlag {
		if pflag.NArg() < 1 {
			pflag.Usage()
			os.Exit(1)
		}
		prefix = pflag.Arg(0)
	}

	// Kubeconfig
	var kubeconfig string
	if env := os.Getenv("KUBECONFIG"); env != "" {
		kubeconfig = env
	} else if home := homedir.HomeDir(); home != "" {
		kubeconfig = filepath.Join(home, ".kube", "config")
	}
	cfg, err := clientcmd.BuildConfigFromFlags("", kubeconfig)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to get kubeconfig: %v\n", err)
		os.Exit(1)
	}
	client, err := dynamic.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to create dynamic client: %v\n", err)
		os.Exit(1)
	}

	// Hämta resurser
	pvbList, err := client.Resource(podVolumeBackupGVR).
		Namespace(*veleroNsFlag).
		List(context.Background(), metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to list PodVolumeBackups: %v\n", err)
		os.Exit(1)
	}

	// Bygg filterlistor
	podNeedles := splitCSV(*podFilterFlag)       // substring, CI
	nsAllowed := splitCSV(*podNSFilterFlag)      // exact
	volumeAllowed := splitCSV(*volumeFilterFlag) // exact

	// Validera output
	outMode := strings.ToLower(strings.TrimSpace(*outputFlag))
	switch outMode {
	case "table", "json", "yaml", "csv", "pretty":
	default:
		fmt.Fprintf(os.Stderr, "Invalid --output: %s (allowed: table|json|yaml|csv|pretty)\n", outMode)
		os.Exit(1)
	}
	colorEnabled := detectColor(*colorFlag)

	// Samla resultat
	rows := make([]row, 0, len(pvbList.Items))
	for _, item := range pvbList.Items {
		name := item.GetName()
		if !*allFlag && !strings.HasPrefix(name, prefix) {
			continue
		}

		var podName, podNS, volume string
		var size *int64
		var sizeHuman string
		var createdHuman string
		var createdRFC string
		labels := item.GetLabels()
		backupName := ""
		if labels != nil {
			backupName = labels["velero.io/backup-name"]
		}

		// Spec
		if spec, found, _ := unstructured.NestedMap(item.Object, "spec"); found {
			if pod, foundPod, _ := unstructured.NestedMap(spec, "pod"); foundPod {
				if n, ok := pod["name"].(string); ok {
					podName = n
				}
				if n, ok := pod["namespace"].(string); ok {
					podNS = n
				}
			}
			if v, ok := spec["volume"].(string); ok {
				volume = v
			}
		}

		// Filtrera
		if len(podNeedles) > 0 && (podName == "" || !anyContainsFold(podName, podNeedles)) {
			continue
		}
		if len(nsAllowed) > 0 && !anyEqual(podNS, nsAllowed) {
			continue
		}
		if len(volumeAllowed) > 0 && !anyEqual(volume, volumeAllowed) {
			continue
		}

		// Storlek: totalBytes, fallback bytesDone
		if status, found, _ := unstructured.NestedMap(item.Object, "status"); found {
			if progress, foundProgress, _ := unstructured.NestedMap(status, "progress"); foundProgress {
				if *debugFlag {
					fmt.Fprintf(os.Stderr, "DEBUG: name=%s, progress=%v\n", item.GetName(), progress)
				}
				if v, ok := progress["totalBytes"]; ok {
					if n, ok2 := getInt64(v); ok2 {
						val := n
						size = &val
						sizeHuman = humanBytes(*size)
					}
				} else if v, ok := progress["bytesDone"]; ok {
					if n, ok2 := getInt64(v); ok2 {
						val := n
						size = &val
						sizeHuman = humanBytes(*size)
					}
				}
			}
		}

		// Created
		if t, found, _ := unstructured.NestedString(item.Object, "metadata", "creationTimestamp"); found {
			createdRFC = t
			if ti, err := time.Parse(time.RFC3339, t); err == nil {
				createdHuman = ti.Format("2006-01-02 15:04:05")
			} else {
				createdHuman = t
			}
		}

		rows = append(rows, row{
			PodName:        podName,
			PodNamespace:   podNS,
			Volume:         volume,
			SizeBytes:      size,
			SizeHuman:      sizeHuman,
			Created:        createdHuman,
			CreatedRFC3339: createdRFC,
			BackupName:     backupName,
			ResourceName:   name,
		})
	}

	// Sortera: podName, sedan volume
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].PodName == rows[j].PodName {
			return rows[i].Volume < rows[j].Volume
		}
		return rows[i].PodName < rows[j].PodName
	})

	// Skriv ut
	switch outMode {
	case "json":
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(rows); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to encode JSON: %v\n", err)
			os.Exit(1)
		}
	case "yaml":
		b, err := yaml.Marshal(rows)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to encode YAML: %v\n", err)
			os.Exit(1)
		}
		_, _ = os.Stdout.Write(b)
	case "csv":
		w := csv.NewWriter(os.Stdout)
		_ = w.Write([]string{"Pod name", "Pod namespace", "Volume", "Size", "Created"})
		for _, r := range rows {
			sizeOut := "-"
			if r.SizeBytes != nil {
				sizeOut = r.SizeHuman
				if sizeOut == "" {
					sizeOut = humanBytes(*r.SizeBytes)
				}
			}
			created := r.Created
			if created == "" && r.CreatedRFC3339 != "" {
				created = r.CreatedRFC3339
			}
			_ = w.Write([]string{r.PodName, r.PodNamespace, r.Volume, sizeOut, created})
		}
		w.Flush()
		if err := w.Error(); err != nil {
			fmt.Fprintf(os.Stderr, "Failed to write CSV: %v\n", err)
			os.Exit(1)
		}
	case "pretty":
		// Vertikal, färgsatt lista
		title := c(colorEnabled, "1;34", "PodVolumeBackup") // bold blue
		fmt.Printf("%s %s\n", title, c(colorEnabled, "2", fmt.Sprintf("(%d items)", len(rows))))
		if len(rows) > 0 {
			fmt.Println(prettyDivider(colorEnabled))
		}
		for i, r := range rows {
			// labels
			lbl := func(s string) string { return c(colorEnabled, "36", s) } // cyan
			val := func(s string) string { return c(colorEnabled, "97", s) } // bright white
			sec := func(s string) string { return c(colorEnabled, "2", s) }  // dim
			bold := func(s string) string { return c(colorEnabled, "1", s) } // bold
			// field values
			sizeOut := "-"
			if r.SizeBytes != nil {
				sizeOut = r.SizeHuman
				if sizeOut == "" {
					sizeOut = humanBytes(*r.SizeBytes)
				}
			}
			created := r.Created
			if created == "" && r.CreatedRFC3339 != "" {
				created = r.CreatedRFC3339
			}
			// lines
			fmt.Printf("%s %s\n", lbl("Backup:    "), bold(val(r.BackupName)))
			fmt.Printf("%s %s\n", lbl("Pod:       "), val(r.PodName))
			fmt.Printf("%s %s\n", lbl("Namespace: "), val(r.PodNamespace))
			fmt.Printf("%s %s\n", lbl("Volume:    "), val(r.Volume))
			fmt.Printf("%s %s", lbl("Size:      "), val(sizeOut))
			if r.SizeBytes != nil {
				fmt.Printf(" %s", sec(fmt.Sprintf("(%d bytes)", *r.SizeBytes)))
			}
			fmt.Println()
			fmt.Printf("%s %s\n", lbl("Created:   "), val(created))
			fmt.Printf("%s %s\n", lbl("Resource:  "), sec(r.ResourceName))

			if i < len(rows)-1 {
				fmt.Println(prettyDivider(colorEnabled))
			}
		}
	default: // table
		w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintln(w, "Pod name\tPod namespace\tVolume\tSize\tCreated")
		for _, r := range rows {
			sizeOut := "-"
			if r.SizeBytes != nil {
				sizeOut = r.SizeHuman
				if sizeOut == "" {
					sizeOut = humanBytes(*r.SizeBytes)
				}
			}
			created := r.Created
			if created == "" && r.CreatedRFC3339 != "" {
				created = r.CreatedRFC3339
			}
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\n",
				r.PodName, r.PodNamespace, r.Volume, sizeOut, created)
		}
		_ = w.Flush()
	}
}
