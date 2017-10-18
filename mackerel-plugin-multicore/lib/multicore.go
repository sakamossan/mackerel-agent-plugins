package mpmulticore

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	mp "github.com/mackerelio/go-mackerel-plugin-helper"
)

var graphDef = map[string]mp.Graphs{
	"multicore.cpu.#": {
		Label: "MultiCore CPU",
		Unit:  "percentage",
		Metrics: []mp.Metrics{
			{Name: "user", Label: "user", Diff: false, Stacked: true},
			{Name: "nice", Label: "nice", Diff: false, Stacked: true},
			{Name: "system", Label: "system", Diff: false, Stacked: true},
			{Name: "idle", Label: "idle", Diff: false, Stacked: true},
			{Name: "iowait", Label: "ioWait", Diff: false, Stacked: true},
			{Name: "irq", Label: "irq", Diff: false, Stacked: true},
			{Name: "softirq", Label: "softirq", Diff: false, Stacked: true},
			{Name: "steal", Label: "steal", Diff: false, Stacked: true},
			{Name: "guest", Label: "guest", Diff: false, Stacked: true},
			{Name: "guest_nice", Label: "guest_nice", Diff: false, Stacked: true},
		},
	},
	"multicore.loadavg_per_core": {
		Label: "MultiCore loadavg5 per core",
		Unit:  "float",
		Metrics: []mp.Metrics{
			{Name: "loadavg5", Label: "loadavg5", Diff: false, Stacked: false},
		},
	},
}

type saveItem struct {
	LastTime       time.Time
	ProcStatsByCPU map[string]procStats
}

type procStats struct {
	User      *uint64 `json:"user"`
	Nice      *uint64 `json:"nice"`
	System    *uint64 `json:"system"`
	Idle      *uint64 `json:"idle"`
	IoWait    *uint64 `json:"iowait"`
	Irq       *uint64 `json:"irq"`
	SoftIrq   *uint64 `json:"softirq"`
	Steal     *uint64 `json:"steal"`
	Guest     *uint64 `json:"guest"`
	GuestNice *uint64 `json:"guest_nice"`
	Total     uint64  `json:"total"`
}

type cpuPercentages struct {
	GroupName string
	User      *float64
	Nice      *float64
	System    *float64
	Idle      *float64
	IoWait    *float64
	Irq       *float64
	SoftIrq   *float64
	Steal     *float64
	Guest     *float64
	GuestNice *float64
}

func parseCounters(values []string) ([]uint64, error) {
	var result []uint64
	for _, v := range values {
		f, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, err
		}
		result = append(result, f)
	}
	return result, nil
}

func fill(arr []uint64, elementCount int) []*uint64 {
	var filled []*uint64
	for _, v := range arr {
		copy := v
		filled = append(filled, &copy)
	}

	if len(arr) < elementCount {
		emptyArray := make([]*uint64, elementCount-len(arr))
		filled = append(filled, emptyArray...)
	}
	return filled
}

func parseProcStat(out io.Reader) (map[string]procStats, error) {
	scanner := bufio.NewScanner(out)
	var result = make(map[string]procStats)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "cpu") {
			continue
		}

		fields := strings.Fields(line)
		key := fields[0]
		values := fields[1:]

		// skip total cpu usage
		if key == "cpu" {
			continue
		}

		counterValues, err := parseCounters(values)
		if err != nil {
			return nil, err
		}

		var total uint64
		for _, v := range counterValues {
			total += v
		}

		filledValues := fill(counterValues, 10)

		result[key] = procStats{
			User:      filledValues[0],
			Nice:      filledValues[1],
			System:    filledValues[2],
			Idle:      filledValues[3],
			IoWait:    filledValues[4],
			Irq:       filledValues[5],
			SoftIrq:   filledValues[6],
			Steal:     filledValues[7],
			Guest:     filledValues[8],
			GuestNice: filledValues[9],
			Total:     total,
		}
	}
	return result, nil
}

func collectProcStatValues() (map[string]procStats, error) {
	file, err := os.Open("/proc/stat")
	if err != nil {
		return nil, err
	}
	defer file.Close()
	return parseProcStat(file)
}

func saveValues(tempFileName string, values map[string]procStats, now time.Time) error {
	f, err := os.Create(tempFileName)
	if err != nil {
		return err
	}
	defer f.Close()

	s := saveItem{
		LastTime:       now,
		ProcStatsByCPU: values,
	}

	encoder := json.NewEncoder(f)
	err = encoder.Encode(s)
	if err != nil {
		return err
	}

	return nil
}

func fetchSavedItem(tempFileName string) (*saveItem, error) {
	f, err := os.Open(tempFileName)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	defer f.Close()

	var stat saveItem
	decoder := json.NewDecoder(f)
	err = decoder.Decode(&stat)
	if err != nil {
		return nil, err
	}
	return &stat, nil
}

func calcCPUUsage(currentValues map[string]procStats, now time.Time, savedItem *saveItem) ([]cpuPercentages, error) {
	if now.Sub(savedItem.LastTime).Seconds() > 600 {
		return nil, fmt.Errorf("Too long duration")
	}

	var result []cpuPercentages
	for key, current := range currentValues {
		last, ok := savedItem.ProcStatsByCPU[key]
		if !ok {
			continue
		}
		user, err := calcPercentage(current.User, last.User, current.Total, last.Total)
		if err != nil {
			return nil, err
		}
		nice, err := calcPercentage(current.Nice, last.Nice, current.Total, last.Total)
		if err != nil {
			return nil, err
		}
		system, err := calcPercentage(current.System, last.System, current.Total, last.Total)
		if err != nil {
			return nil, err
		}
		idle, err := calcPercentage(current.Idle, last.Idle, current.Total, last.Total)
		if err != nil {
			return nil, err
		}
		iowait, err := calcPercentage(current.IoWait, last.IoWait, current.Total, last.Total)
		if err != nil {
			return nil, err
		}
		irq, err := calcPercentage(current.Irq, last.Irq, current.Total, last.Total)
		if err != nil {
			return nil, err
		}
		softirq, err := calcPercentage(current.SoftIrq, last.SoftIrq, current.Total, last.Total)
		if err != nil {
			return nil, err
		}
		steal, err := calcPercentage(current.Steal, last.Steal, current.Total, last.Total)
		if err != nil {
			return nil, err
		}
		guest, err := calcPercentage(current.Guest, last.Guest, current.Total, last.Total)
		if err != nil {
			return nil, err
		}
		guestNice, err := calcPercentage(current.GuestNice, last.GuestNice, current.Total, last.Total)
		if err != nil {
			return nil, err
		}

		result = append(result, cpuPercentages{
			GroupName: key,
			User:      user,
			Nice:      nice,
			System:    system,
			Idle:      idle,
			IoWait:    iowait,
			Irq:       irq,
			SoftIrq:   softirq,
			Steal:     steal,
			Guest:     guest,
			GuestNice: guestNice,
		})
	}

	return result, nil
}

func calcPercentage(currentValue *uint64, lastValue *uint64, currentTotal uint64, lastTotal uint64) (*float64, error) {
	if currentValue == nil || lastValue == nil {
		return nil, nil
	}

	if *lastValue > *currentValue || lastTotal > currentTotal {
		return nil, errors.New("counter may be reset")
	}

	ret := float64(*currentValue-*lastValue) / float64(currentTotal-lastTotal) * 100.0
	return &ret, nil
}

func fetchLoadavg5() (float64, error) {
	contentbytes, err := ioutil.ReadFile("/proc/loadavg")
	if err != nil {
		return 0.0, err
	}
	content := string(contentbytes)
	cols := strings.Fields(content)

	if len(cols) > 2 {
		f, err := strconv.ParseFloat(cols[1], 64)
		if err != nil {
			return 0.0, err
		}
		return f, nil
	}
	return 0.0, fmt.Errorf("cannot fetch loadavg5")
}

func printValue(key string, value *float64, time time.Time) {
	if value != nil {
		fmt.Printf("%s\t%f\t%d\n", key, *value, time.Unix())
	}
}

func outputCPUUsage(cpuUsage []cpuPercentages, now time.Time) {
	for _, u := range cpuUsage {
		printValue(fmt.Sprintf("multicore.cpu.%s.user", u.GroupName), u.User, now)
		printValue(fmt.Sprintf("multicore.cpu.%s.nice", u.GroupName), u.Nice, now)
		printValue(fmt.Sprintf("multicore.cpu.%s.system", u.GroupName), u.System, now)
		printValue(fmt.Sprintf("multicore.cpu.%s.idle", u.GroupName), u.Idle, now)
		printValue(fmt.Sprintf("multicore.cpu.%s.iowait", u.GroupName), u.IoWait, now)
		printValue(fmt.Sprintf("multicore.cpu.%s.irq", u.GroupName), u.Irq, now)
		printValue(fmt.Sprintf("multicore.cpu.%s.softirq", u.GroupName), u.SoftIrq, now)
		printValue(fmt.Sprintf("multicore.cpu.%s.steal", u.GroupName), u.Steal, now)
		printValue(fmt.Sprintf("multicore.cpu.%s.guest", u.GroupName), u.Guest, now)
		printValue(fmt.Sprintf("multicore.cpu.%s.guest_nice", u.GroupName), u.GuestNice, now)
	}
}

func outputLoadavgPerCore(loadavgPerCore float64, now time.Time) {
	printValue("multicore.loadavg_per_core.loadavg5", &loadavgPerCore, now)
}

func outputDefinitions() {
	fmt.Println("# mackerel-agent-plugin")
	var graphs mp.GraphDef
	graphs.Graphs = graphDef

	b, err := json.Marshal(graphs)
	if err != nil {
		log.Fatalln("OutputDefinitions: ", err)
	}
	fmt.Println(string(b))
}

func outputMulticore(tempFileName string) {
	now := time.Now()

	currentValues, err := collectProcStatValues()
	if err != nil {
		log.Fatalln("collectProcStatValues: ", err)
	}

	savedItem, err := fetchSavedItem(tempFileName)
	saveValues(tempFileName, currentValues, now)
	if err != nil {
		log.Fatalln("fetchLastValues: ", err)
	}

	// maybe first time run
	if savedItem == nil {
		return
	}

	cpuUsage, err := calcCPUUsage(currentValues, now, savedItem)
	if err != nil {
		log.Fatalln("calcCPUUsage: ", err)
	}

	loadavg5, err := fetchLoadavg5()
	if err != nil {
		log.Fatalln("fetchLoadavg5: ", err)
	}
	loadPerCPUCount := loadavg5 / (float64(len(cpuUsage)))

	outputCPUUsage(cpuUsage, now)
	outputLoadavgPerCore(loadPerCPUCount, now)
}

func generateTempfilePath() string {
	dir := os.Getenv("MACKEREL_PLUGIN_WORKDIR")
	if dir == "" {
		dir = os.TempDir()
	}
	return filepath.Join(dir, "mackerel-plugin-multicore")
}

// Do the plugin
func Do() {
	var tempFileName string
	optTempfile := flag.String("tempfile", "", "Temp file name")
	flag.Parse()

	tempFileName = *optTempfile
	if tempFileName == "" {
		tempFileName = generateTempfilePath()
	}

	if os.Getenv("MACKEREL_AGENT_PLUGIN_META") != "" {
		outputDefinitions()
	} else {
		outputMulticore(tempFileName)
	}
}
