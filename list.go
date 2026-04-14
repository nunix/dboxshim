package main

import (
	"bufio"
	"crypto/md5"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var projectFlag string
var projectEphemeralFlag bool

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "List distrobox instances",
	Run: func(cmd *cobra.Command, args []string) {
		runList()
	},
}

func init() {
	rootCmd.PersistentFlags().StringVarP(&projectFlag, "project", "p", "", "Run project directly from INI file")
	rootCmd.PersistentFlags().BoolVarP(&projectEphemeralFlag, "ephemeral", "e", false, "Execute project in ephemeral mode (default is permanent)")

	rootCmd.AddCommand(listCmd)
	rootCmd.Run = func(cmd *cobra.Command, args []string) {
		runList()
	}
}

type Instance struct {
	Type        string
	ID          string
	Name        string
	StatusEmoji string
	StatusFull  string
	Image       string
	Exports     string
	IsProject   string
}

type ProjectFile struct {
	Name     string
	Path     string
	Content  string
	RepoPath string
	RepoURL  string
	RepoName string
}

func parseDistroboxList(out string, ttype string, projectNames map[string]bool) []Instance {
	var rows []Instance
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(strings.TrimSpace(line), "ID") || strings.HasPrefix(strings.TrimSpace(line), "WARN") {
			continue
		}
		parts := strings.Split(line, "|")
		if len(parts) >= 4 {
			id := strings.TrimSpace(parts[0])
			name := strings.TrimSpace(parts[1])
			rawStatus := strings.TrimSpace(parts[2])
			statusEmoji := "🔴"
			if strings.HasPrefix(rawStatus, "Up") {
				statusEmoji = "🟢"
			}
			image := strings.TrimSpace(parts[3])
			exports := getExportsFast(name, ttype == "Root")
			isProj := "   "
			if projectNames[name] {
				isProj = "✅ "
			}
			rows = append(rows, Instance{ttype, id, name, statusEmoji, rawStatus, image, exports, isProj})
		}
	}
	return rows
}

func getExportsFast(name string, isRoot bool) string {
	apps, bins := getExportLists(name, isRoot)
	total := len(apps) + len(bins)
	if total == 0 {
		return "-"
	}
	return fmt.Sprintf("%d   ", total)
}

func getExportLists(name string, isRoot bool) ([]string, []string) {
	appsDir := os.ExpandEnv("$HOME/.local/share/applications")
	if isRoot {
		appsDir = "/usr/local/share/applications"
	}
	var apps []string
	files, err := os.ReadDir(appsDir)
	if err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".desktop") && !f.IsDir() {
				content, err := os.ReadFile(appsDir + "/" + f.Name())
				if err == nil && strings.Contains(string(content), "# name: "+name) {
					appName := f.Name()
					for _, line := range strings.Split(string(content), "\n") {
						if strings.HasPrefix(line, "Name=") {
							appName = strings.TrimPrefix(line, "Name=")
							break
						}
					}
					apps = append(apps, appName)
				}
			}
		}
	}
	binsDir := os.ExpandEnv("$HOME/.local/bin")
	if isRoot {
		binsDir = "/usr/local/bin"
	}
	var bins []string
	files, err = os.ReadDir(binsDir)
	if err == nil {
		for _, f := range files {
			if !f.IsDir() {
				content, err := os.ReadFile(binsDir + "/" + f.Name())
				if err == nil && strings.Contains(string(content), "# name: "+name) {
					bins = append(bins, f.Name())
				}
			}
		}
	}
	return apps, bins
}

func getDetailText(inst Instance) string {
	apps, bins := getExportLists(inst.Name, inst.Type == "Root")
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("[green::b]📦 Details for %s[-::-]\n\n", inst.Name))
	sb.WriteString(fmt.Sprintf("[yellow]Type:[white] %s\n", inst.Type))
	sb.WriteString(fmt.Sprintf("[yellow]ID:[white] %s\n", inst.ID))
	if strings.TrimSpace(inst.IsProject) == "✅" {
		sb.WriteString("[yellow]Project:[white] ✅ Based on local INI file\n")
	}
	sb.WriteString(fmt.Sprintf("[yellow]Status:[white] %s %s\n", inst.StatusEmoji, inst.StatusFull))
	sb.WriteString(fmt.Sprintf("[yellow]Image:[white] %s\n\n", inst.Image))

	sb.WriteString("[green::b]📦 Exported items:[-::-]        \n")
	if len(apps) > 0 || len(bins) > 0 {
		if len(apps) > 0 {
			for _, app := range apps {
				sb.WriteString(fmt.Sprintf("  📱 %s        \n", app))
			}
		}
		if len(bins) > 0 {
			for _, bin := range bins {
				sb.WriteString(fmt.Sprintf("  ⚙️ %s        \n", bin))
			}
		}
		sb.WriteString("\n")
	} else {
		sb.WriteString("  No exports available.        \n\n")
	}
	return sb.String()
}

func getVersions() (string, string) {
	dbox := "Distrobox N/A"
	if out, err := exec.Command("distrobox", "--version").Output(); err == nil {
		dbox = strings.TrimSpace(string(out))
	}
	rt := "Runtime N/A"
	if out, err := exec.Command("podman", "--version").Output(); err == nil {
		parts := strings.Split(strings.TrimSpace(string(out)), " ")
		if len(parts) >= 3 {
			rt = parts[0] + " " + parts[2]
		} else {
			rt = string(out)
		}
	} else if out, err := exec.Command("docker", "--version").Output(); err == nil {
		rt = strings.TrimSpace(string(out))
	}
	return dbox, strings.TrimSpace(rt)
}

func startResourceMonitor(app *tview.Application, statusRight *tview.TextView) {
	go func() {
		for {
			cpuTotal := 0.0
			memTotalMB := 0.0
			gpuPerc := "--"
			if out, err := exec.Command("podman", "stats", "-a", "--no-stream", "--format", "{{.CPUPerc}}|{{.MemUsage}}").Output(); err == nil {
				lines := strings.Split(string(out), "\n")
				for _, line := range lines {
					if !strings.Contains(line, "|") {
						continue
					}
					parts := strings.Split(line, "|")
					if len(parts) != 2 {
						continue
					}
					cpuStr := strings.TrimSpace(strings.TrimSuffix(parts[0], "%"))
					var cpu float64
					fmt.Sscanf(cpuStr, "%f", &cpu)
					cpuTotal += cpu
					memStr := strings.Split(parts[1], " / ")[0]
					memStr = strings.TrimSpace(memStr)
					val := 0.0
					if strings.HasSuffix(memStr, "kB") {
						fmt.Sscanf(strings.TrimSuffix(memStr, "kB"), "%f", &val)
						memTotalMB += val / 1024.0
					} else if strings.HasSuffix(memStr, "MB") {
						fmt.Sscanf(strings.TrimSuffix(memStr, "MB"), "%f", &val)
						memTotalMB += val
					} else if strings.HasSuffix(memStr, "GB") {
						fmt.Sscanf(strings.TrimSuffix(memStr, "GB"), "%f", &val)
						memTotalMB += val * 1024.0
					} else if strings.HasSuffix(memStr, "B") {
						fmt.Sscanf(strings.TrimSuffix(memStr, "B"), "%f", &val)
						memTotalMB += val / (1024.0 * 1024.0)
					}
				}
			}
			if out, err := exec.Command("rocm-smi", "--showuse").Output(); err == nil {
				lines := strings.Split(string(out), "\n")
				for _, line := range lines {
					if strings.Contains(line, "GPU use (%)") {
						parts := strings.Split(line, ":")
						if len(parts) > 0 {
							gpuPerc = strings.TrimSpace(parts[len(parts)-1])
						}
					}
				}
			} else if out, err := exec.Command("nvidia-smi", "--query-gpu=utilization.gpu", "--format=csv,noheader,nounits").Output(); err == nil {
				gpuPerc = strings.TrimSpace(string(out))
			}
			statsStr := fmt.Sprintf(" [white:#004b23] ⚡ CPU: %.1f%% | 🧠 RAM: %.0fMB | 🎮 GPU: %s%% [-:-] ", cpuTotal, memTotalMB, gpuPerc)
			app.QueueUpdateDraw(func() {
				statusRight.SetText(statsStr)
			})
			time.Sleep(5 * time.Second)
		}
	}()
}

func loadProjects() []ProjectFile {
	var projects []ProjectFile
	files, err := os.ReadDir(".")
	if err == nil {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".ini") && !f.IsDir() {
				path := filepath.Join(".", f.Name())
				content, _ := os.ReadFile(path)
				projects = append(projects, ProjectFile{
					Name:    f.Name(),
					Path:    path,
					Content: string(content),
				})
			}
		}
	}

	// Also load remote projects from temp directory
	tempBaseDir := filepath.Join(os.TempDir(), "dboxshim")
	filepath.WalkDir(tempBaseDir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if d.Name() == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), ".ini") {
			content, _ := os.ReadFile(path)
			fullPath, _ := filepath.Abs(path)
			projects = append(projects, ProjectFile{
				Name:    "🌐 " + d.Name(),
				Path:    fullPath,
				Content: string(content),
			})
		}
		return nil
	})

	return projects
}

func parseIniFile(filepath string) ([]string, string) {
	file, err := os.Open(filepath)
	if err != nil {
		return nil, ""
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	var name string
	var args []string
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			name = line[1 : len(line)-1]
			args = append(args, "--name", name)
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) == 2 {
			k := strings.TrimSpace(parts[0])
			v := strings.TrimSpace(parts[1])
			if strings.HasPrefix(v, "\"") && strings.HasSuffix(v, "\"") {
				v = v[1 : len(v)-1]
			} else if strings.HasPrefix(v, "'") && strings.HasSuffix(v, "'") {
				v = v[1 : len(v)-1]
			}
			switch k {
			case "image":
				args = append(args, "--image", v)
			case "additional_packages":
				args = append(args, "--additional-packages", v)
			case "init_hooks":
				args = append(args, "--init-hooks", v)
			case "pre_init_hooks":
				args = append(args, "--pre-init-hooks", v)
			case "volume":
				args = append(args, "--volume", v)
			case "additional_flags":
				args = append(args, "--additional-flags", v)
			case "home":
				args = append(args, "--home", v)
			}
		}
	}
	return args, name
}

func fetchGitRepo(urlStr string) ([]ProjectFile, error) {
	var repoPath string
	
	parseUrl := urlStr
	if !strings.Contains(urlStr, "://") && !strings.HasPrefix(urlStr, "git@") {
		parseUrl = "https://" + urlStr
	} else if strings.HasPrefix(urlStr, "git@") {
		parseUrl = "ssh://" + strings.Replace(urlStr, ":", "/", 1)
	}

	parsedURL, err := url.Parse(parseUrl)
	if err == nil && parsedURL.Host != "" {
		cleanPath := strings.TrimSuffix(parsedURL.Path, ".git")
		repoPath = filepath.Join(os.TempDir(), "dboxshim", "repos", strings.ToLower(parsedURL.Host), strings.ToLower(cleanPath))
	} else {
		hash := fmt.Sprintf("%x", md5.Sum([]byte(strings.ToLower(urlStr))))
		repoPath = filepath.Join(os.TempDir(), "dboxshim", "repos", hash)
	}

	gitDir := filepath.Join(repoPath, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		os.RemoveAll(repoPath)
		err := os.MkdirAll(filepath.Dir(repoPath), 0755)
		if err != nil {
			return nil, err
		}
		
		cloneUrl := urlStr
		if !strings.Contains(urlStr, "://") && !strings.HasPrefix(urlStr, "git@") {
			cloneUrl = "https://" + urlStr
		}
		
		cmd := exec.Command("git", "clone", "--filter=blob:none", "--no-checkout", "--depth=1", cloneUrl, repoPath)
		if output, err := cmd.CombinedOutput(); err != nil {
			// Handle error 128 or other clone errors by trying a unique path
			if strings.Contains(err.Error(), "exit status 128") || strings.Contains(string(output), "already exists") {
				repoPath = repoPath + "_" + fmt.Sprintf("%d", time.Now().UnixNano())
				cmdRetry := exec.Command("git", "clone", "--filter=blob:none", "--no-checkout", "--depth=1", cloneUrl, repoPath)
				if outRetry, errRetry := cmdRetry.CombinedOutput(); errRetry != nil {
					return nil, fmt.Errorf("failed to clone repository after retry: %v, output: %s", errRetry, string(outRetry))
				}
			} else {
				return nil, fmt.Errorf("failed to clone repository: %v, output: %s", err, string(output))
			}
		}
	} else {
		cmd := exec.Command("git", "pull")
		cmd.Dir = repoPath
		cmd.Run()
	}

	out, err := exec.Command("git", "-C", repoPath, "ls-tree", "-r", "--name-only", "HEAD").Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list repository files: %v", err)
	}

	var projects []ProjectFile
	lines := strings.Split(string(out), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasSuffix(line, ".ini") {
			exec.Command("git", "-C", repoPath, "checkout", "HEAD", "--", line).Run()
			fullPath := filepath.Join(repoPath, line)
			content, err := os.ReadFile(fullPath)
			if err == nil {
				repoDomainPath := strings.TrimPrefix(parseUrl, "https://")
				repoDomainPath = strings.TrimPrefix(repoDomainPath, "http://")
				repoDomainPath = strings.TrimPrefix(repoDomainPath, "ssh://")
				repoDomainPath = strings.TrimSuffix(repoDomainPath, ".git")
				projects = append(projects, ProjectFile{
					Name:     "🌐 " + filepath.Base(line) + " (" + repoDomainPath + ")",
					Path:     fullPath,
					Content:  string(content),
					RepoPath: repoPath,
					RepoURL:  urlStr,
					RepoName: repoDomainPath,
				})
			}
		}
	}
	if len(projects) == 0 {
		return nil, fmt.Errorf("no .ini files found in repository")
	}
	return projects, nil
}

func fetchRemoteIni(urlStr string) (string, string, error) {
	resp, err := http.Get(urlStr)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}

	parts := strings.Split(urlStr, "/")
	fileName := parts[len(parts)-1]
	if fileName == "" || !strings.Contains(fileName, ".ini") {
		fileName = "remote.ini"
	}

	return fileName, string(body), nil
}

func runList() {
	var userInstances []Instance
	var rootInstances []Instance
	var projectFiles []ProjectFile
	var remoteProjects []ProjectFile

	projectFiles = loadProjects()
	seenPaths := make(map[string]bool)
	var dedupedProjects []ProjectFile
	for _, p := range projectFiles {
		if !seenPaths[p.Path] {
			seenPaths[p.Path] = true
			dedupedProjects = append(dedupedProjects, p)
		}
	}
	for _, p := range remoteProjects {
		if !seenPaths[p.Path] {
			seenPaths[p.Path] = true
			dedupedProjects = append(dedupedProjects, p)
		}
	}
	projectFiles = dedupedProjects
	projectNames := make(map[string]bool)
	for _, proj := range projectFiles {
		_, name := parseIniFile(proj.Path)
		if name != "" {
			projectNames[name] = true
		}
		
		fileName := filepath.Base(proj.Path)
		if strings.HasSuffix(fileName, ".ini") {
			projectNames[strings.TrimSuffix(fileName, ".ini")] = true
		}
	}

	out, _ := exec.Command("distrobox", "list", "--no-color").Output()
	userInstances = parseDistroboxList(string(out), "User", projectNames)

	outRoot, _ := exec.Command("distrobox", "list", "--root", "--no-color").Output()
	rootInstances = parseDistroboxList(string(outRoot), "Root", projectNames)

	var selectedInstance *Instance
	var selectedAction string
	var selectedProject *ProjectFile

	if projectFlag != "" {
		absPath, err := filepath.Abs(projectFlag)
		if err != nil {
			fmt.Println("Invalid project path:", err)
			return
		}
		if projectEphemeralFlag {
			selectedAction = "ephemeral"
		} else {
			selectedAction = "permanent"
		}
		selectedProject = &ProjectFile{
			Name: filepath.Base(absPath),
			Path: absPath,
		}
	} else {

		app := tview.NewApplication().EnableMouse(true)
		pages := tview.NewPages()

		table := tview.NewTable().
			SetBorders(false).
			SetSelectable(true, false).
			SetFixed(1, 0)

		tree := tview.NewTreeView()
		tree.SetBorder(true).SetTitle(" 📂 Project Files (*.ini) ").SetTitleColor(tcell.ColorForestGreen)

		leftPages := tview.NewPages()
		leftPages.AddPage("table", table, true, true)
		leftPages.AddPage("tree", tree, true, false)

		table.SetSelectedStyle(tcell.StyleDefault.Foreground(tcell.ColorBlack).Background(tcell.ColorWhite))
		table.SetBorder(true).SetTitle(" 📦 Distrobox Instances ").SetTitleColor(tcell.ColorForestGreen)

		detailsBox := tview.NewTextView().
			SetDynamicColors(true).
			SetRegions(true).
			SetWrap(true).
			SetWordWrap(true)
		detailsBox.SetBorder(true).SetTitle(" 🔍 Details ").SetTitleColor(tcell.ColorForestGreen)
		commandsBox := tview.NewTextView().
			SetDynamicColors(true).
			SetRegions(true).
			SetWrap(true).
			SetWordWrap(true)
		commandsBox.SetBorder(true).SetTitle(" ⌨️  Commands ").SetTitleColor(tcell.ColorForestGreen)
		commandsBox.SetText("[green]↑/↓/j/k:[white] Navigate\n[green]Enter:[white] Select/Enter\n[green]Space:[white] Start/Stop\n[green]d:[white] Delete\n[green]u/r/p:[white] Switch Tabs\n[green]←/→ or h/l:[white] Prev/Next Tab\n[green]o:[white] Open URL\n[green]q/ESC:[white] Quit")

		rightFlex := tview.NewFlex().
			SetDirection(tview.FlexRow).
			AddItem(detailsBox, 0, 3, false).
			AddItem(commandsBox, 11, 1, false)

		tabs := tview.NewTextView().
			SetDynamicColors(true).
			SetRegions(true).
			SetWrap(false).
			SetTextAlign(tview.AlignCenter)

		var switchTab func(string)
		tabs.SetHighlightedFunc(func(added, removed, remaining []string) {
			if len(added) > 0 {
				switchTab(added[0])
				tabs.Highlight("")
			}
		})

		currentTab := "user"
		var currentInstances []Instance

				updateDetails := func(row int) {
			detailsBox.Clear()
			if currentTab == "projects" {
				// Handled by tree selection changed
				return
			}
			if row < 1 || row > len(currentInstances) {
				return
			}
			inst := currentInstances[row-1]
			detailsBox.SetText(getDetailText(inst))
		}

				drawTree := func() {
			root := tview.NewTreeNode("Projects").SetExpanded(true)
			tree.SetRoot(root).SetCurrentNode(root)
			
			localNode := tview.NewTreeNode("🏠 Local Workspace").SetExpanded(true).SetSelectable(true).SetColor(tcell.ColorYellow)
			remoteNodes := make(map[string]*tview.TreeNode)
			
			for i := range projectFiles {
				proj := &projectFiles[i]
				if proj.RepoName == "" {
					node := tview.NewTreeNode("📄 " + proj.Name).SetReference(proj).SetSelectable(true).SetColor(tcell.ColorWhite)
					localNode.AddChild(node)
				} else {
					repoKey := proj.RepoName
					if _, exists := remoteNodes[repoKey]; !exists {
						rNode := tview.NewTreeNode("🌐 " + repoKey).SetExpanded(true).SetSelectable(true).SetColor(tcell.ColorDarkCyan)
						remoteNodes[repoKey] = rNode
					}
					
					var relPath string
					if proj.RepoPath != "" {
						var err error
						relPath, err = filepath.Rel(proj.RepoPath, proj.Path)
						if err != nil || relPath == "." {
							relPath = filepath.Base(proj.Path)
						}
					} else {
						relPath = filepath.Base(proj.Path)
					}
					
					parts := strings.Split(relPath, string(filepath.Separator))
					curr := remoteNodes[repoKey]
					for j, part := range parts {
						if j == len(parts)-1 {
							node := tview.NewTreeNode("📄 " + part).SetReference(proj).SetSelectable(true).SetColor(tcell.ColorWhite)
							curr.AddChild(node)
						} else {
							var childNode *tview.TreeNode
							for _, child := range curr.GetChildren() {
								if child.GetText() == "📁 " + part {
									childNode = child
									break
								}
							}
							if childNode == nil {
								childNode = tview.NewTreeNode("📁 " + part).SetExpanded(true).SetSelectable(true).SetColor(tcell.ColorBlue)
								curr.AddChild(childNode)
							}
							curr = childNode
						}
					}
				}
			}
			
			if len(localNode.GetChildren()) > 0 {
				root.AddChild(localNode)
			}
			for _, rNode := range remoteNodes {
				root.AddChild(rNode)
			}
		}

		drawTable := func() {
			table.Clear()
			table.SetTitle(" 📦 Distrobox Instances ")
			headers := []string{"ID", "Name", "Project", "Status", "Exports"}
			for col, header := range headers {
				table.SetCell(0, col, tview.NewTableCell(header).SetExpansion(1).SetTextColor(tcell.ColorGreen).SetSelectable(false).SetAlign(tview.AlignLeft))
			}
			if len(currentInstances) == 0 {
				table.SetCell(1, 0, tview.NewTableCell("No instances found.").SetTextColor(tcell.ColorGray).SetSelectable(false))
				detailsBox.SetText("")
				return
			}
			for row, inst := range currentInstances {
				table.SetCell(row+1, 0, tview.NewTableCell(inst.ID).SetTextColor(tcell.ColorWhite).SetExpansion(1))
				table.SetCell(row+1, 1, tview.NewTableCell(inst.Name).SetTextColor(tcell.ColorYellow).SetExpansion(1))
				table.SetCell(row+1, 2, tview.NewTableCell(inst.IsProject).SetTextColor(tcell.ColorWhite).SetExpansion(1))
				table.SetCell(row+1, 3, tview.NewTableCell(inst.StatusEmoji).SetTextColor(tcell.ColorLightBlue).SetExpansion(1))
				table.SetCell(row+1, 4, tview.NewTableCell(inst.Exports).SetTextColor(tcell.ColorWhite).SetExpansion(1))
			}
			table.Select(1, 0)
			updateDetails(1)
		}

		drawTabs := func() {
			userStyle := "[gray:black]"
			rootStyle := "[gray:black]"
			projStyle := "[gray:black]"

			if currentTab == "user" {
				userStyle = "[black:#30ba78]"
			} else if currentTab == "root" {
				rootStyle = "[white:#8b0000]"
			} else if currentTab == "projects" {
				projStyle = "[black:#008b8b]" // Dark cyan for projects
			}
			tabs.SetText(fmt.Sprintf(`["user"]%s  User Instances (u)  [-:-][""]    ["root"]%s  Root Instances (r)  [-:-][""]    ["projects"]%s  Projects (p)  [-:-][""]`, userStyle, rootStyle, projStyle))
		}

		
		tree.SetChangedFunc(func(node *tview.TreeNode) {
			ref := node.GetReference()
			if ref != nil {
				proj := ref.(*ProjectFile)
				detailsBox.SetText(fmt.Sprintf("[green::b]📄 %s[-::-]\n\n[white]%s", proj.Name, proj.Content))
			} else {
				detailsBox.SetText("")
			}
		})
		
		var modal *tview.Modal
		tree.SetSelectedFunc(func(node *tview.TreeNode) {
			ref := node.GetReference()
			if ref != nil {
				selectedProject = ref.(*ProjectFile)
				pages.ShowPage("modal")
				app.SetFocus(modal)
			} else {
				node.SetExpanded(!node.IsExpanded())
			}
		})

				switchTab = func(tab string) {
			currentTab = tab
			if tab == "user" {
				currentInstances = userInstances
				leftPages.SwitchToPage("table")
				app.SetFocus(table)
				drawTable()
			} else if tab == "root" {
				currentInstances = rootInstances
				leftPages.SwitchToPage("table")
				app.SetFocus(table)
				drawTable()
			} else if tab == "projects" {
				leftPages.SwitchToPage("tree")
				app.SetFocus(tree)
				drawTree()
			}
			drawTabs()
		}
		refreshInstances := func() {
			projectFiles = loadProjects()
			seenPaths := make(map[string]bool)
			var dedupedProjects []ProjectFile
			for _, p := range projectFiles {
				if !seenPaths[p.Path] {
					seenPaths[p.Path] = true
					dedupedProjects = append(dedupedProjects, p)
				}
			}
			for _, p := range remoteProjects {
				if !seenPaths[p.Path] {
					seenPaths[p.Path] = true
					dedupedProjects = append(dedupedProjects, p)
				}
			}
			projectFiles = dedupedProjects
			projectNames := make(map[string]bool)
			for _, proj := range projectFiles {
				_, name := parseIniFile(proj.Path)
				if name != "" {
					projectNames[name] = true
				}
				
				fileName := filepath.Base(proj.Path)
				if strings.HasSuffix(fileName, ".ini") {
					projectNames[strings.TrimSuffix(fileName, ".ini")] = true
				}
			}
			out, _ := exec.Command("distrobox", "list", "--no-color").Output()
			userInstances = parseDistroboxList(string(out), "User", projectNames)
			outRoot, _ := exec.Command("distrobox", "list", "--root", "--no-color").Output()
			rootInstances = parseDistroboxList(string(outRoot), "Root", projectNames)
			switchTab(currentTab)
		}

		switchTab("user")

		table.SetSelectionChangedFunc(func(row, column int) {
			if row < 1 {
				table.Select(1, 0)
				return
			}
			updateDetails(row)
		})

		

		modal = tview.NewModal().
			SetText("Create Instance from INI").
			AddButtons([]string{"Permanent", "Ephemeral", "Cancel"}).
			SetDoneFunc(func(buttonIndex int, buttonLabel string) {
				if buttonLabel == "Permanent" {
					selectedAction = "permanent"
					app.Stop()
				} else if buttonLabel == "Ephemeral" {
					selectedAction = "ephemeral"
					app.Stop()
				} else {
					pages.HidePage("modal")
					app.SetFocus(table)
				}
			})

		handleTableSelect := func(row, column int) {
			if currentTab == "projects" {
				if row < 1 || row > len(projectFiles) {
					return
				}
				selectedProject = &projectFiles[row-1]
				pages.ShowPage("modal")
				app.SetFocus(modal)
			} else {
				if row < 1 || row > len(currentInstances) {
					return
				}
				selectedInstance = &currentInstances[row-1]
				selectedAction = "enter"
				app.Stop()
			}
		}

		table.SetSelectedFunc(handleTableSelect)

		table.SetMouseCapture(func(action tview.MouseAction, event *tcell.EventMouse) (tview.MouseAction, *tcell.EventMouse) {
			if action == tview.MouseLeftDoubleClick {
				row, col := table.GetSelection()
				handleTableSelect(row, col)
				return action, nil
			}
			return action, event
		})

		contentFlex := tview.NewFlex().
			AddItem(leftPages, 0, 2, true).
			AddItem(rightFlex, 0, 1, false)

		dbV, rtV := getVersions()

		statusCenter := tview.NewTextView().SetDynamicColors(true).SetRegions(false).SetWrap(false).SetTextAlign(tview.AlignLeft)
		statusCenter.SetText(fmt.Sprintf(" [white:#004b23] 📦 %s | 🐳 %s [-:-] ", dbV, rtV))

		statusRight := tview.NewTextView().SetDynamicColors(true).SetRegions(false).SetWrap(false).SetTextAlign(tview.AlignRight)
		statusRight.SetText(" [white:#004b23] ⚡ CPU: --.-% | 🧠 RAM: ---MB | 🎮 GPU: --% [-:-] ")

		startResourceMonitor(app, statusRight)

		statusBar := tview.NewFlex().
			AddItem(statusCenter, 0, 1, false).
			AddItem(tview.NewBox(), 0, 1, false).
			AddItem(statusRight, 0, 1, false)

		mainFlex := tview.NewFlex().
			SetDirection(tview.FlexRow).
			AddItem(tabs, 1, 0, false).
			AddItem(contentFlex, 0, 1, true).
			AddItem(statusBar, 1, 0, false)

		pages.AddPage("main", mainFlex, true, true)
		pages.AddPage("modal", modal, false, false)

		app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		if name, _ := pages.GetFrontPage(); name != "main" {
			return event
		}

			if event.Rune() == ' ' {
				if currentTab == "user" || currentTab == "root" {
					row, _ := table.GetSelection()
					if row >= 1 && row <= len(currentInstances) {
						inst := currentInstances[row-1]
						go func() {
							if inst.StatusEmoji == "🟢" {
								args := []string{"stop", "--yes", inst.Name}
								if inst.Type == "Root" {
									args = append([]string{"--root"}, args...)
								}
								exec.Command("distrobox", args...).Run()
							} else {
								args := []string{"enter", "-T", "-n", inst.Name, "--", "true"}
								if inst.Type == "Root" {
									args = append([]string{"--root"}, args...)
								}
								exec.Command("distrobox", args...).Run()
							}
							app.QueueUpdateDraw(func() {
								refreshInstances()
							})
						}()
					}
				}
				return nil
			}

			if event.Rune() == 'd' {
				if currentTab == "user" || currentTab == "root" {
					row, _ := table.GetSelection()
					if row >= 1 && row <= len(currentInstances) {
						inst := currentInstances[row-1]

						deleteModal := tview.NewModal().
							SetText(fmt.Sprintf("Are you sure you want to delete '%s'?", inst.Name)).
							AddButtons([]string{"Delete", "Cancel"}).
							SetDoneFunc(func(buttonIndex int, buttonLabel string) {
								pages.RemovePage("deleteModal")
								if buttonLabel == "Delete" {
									waitModal := tview.NewModal().
										SetText(fmt.Sprintf("Deleting '%s', please wait...", inst.Name))
									pages.AddPage("waitModal", waitModal, false, false)
									pages.ShowPage("waitModal")
									app.SetFocus(waitModal)

									go func() {
										args := []string{"rm", "-f", inst.Name}
										if inst.Type == "Root" {
											args = append([]string{"--root"}, args...)
										}
										exec.Command("distrobox", args...).Run()

										app.QueueUpdateDraw(func() {
											pages.RemovePage("waitModal")
											refreshInstances()
											app.SetFocus(table)
										})
									}()
								} else {
									app.SetFocus(table)
								}
							})
						pages.AddPage("deleteModal", deleteModal, false, false)
						pages.ShowPage("deleteModal")
						app.SetFocus(deleteModal)
					}
				}
				return nil
			}

			if name, _ := pages.GetFrontPage(); name != "main" {
				return event
			}

			if (currentTab == "user" || currentTab == "root") && len(currentInstances) == 0 {
				if event.Key() == tcell.KeyUp || event.Key() == tcell.KeyDown || event.Key() == tcell.KeyPgUp || event.Key() == tcell.KeyPgDn || event.Rune() == 'j' || event.Rune() == 'k' {
					return nil
				}

			} else {
				if currentTab == "projects" {
					// Let tree handle its own vim navigation
					if event.Rune() == 'j' {
						return tcell.NewEventKey(tcell.KeyDown, 0, tcell.ModNone)
					} else if event.Rune() == 'k' {
						return tcell.NewEventKey(tcell.KeyUp, 0, tcell.ModNone)
					}
					return event
				}
				limit := len(currentInstances)
				if event.Key() == tcell.KeyUp || event.Rune() == 'k' {
					row, _ := table.GetSelection()
					if row <= 1 {
						table.Select(limit, 0)
						return nil
					}
				}
				if event.Key() == tcell.KeyDown || event.Rune() == 'j' {
					row, _ := table.GetSelection()
					if row >= limit {
						table.Select(1, 0)
						return nil
					}
				}
			}

			if event.Key() == tcell.KeyEscape || event.Rune() == 'q' {
				app.Stop()
				return nil
			}
			if event.Rune() == 'u' {
				switchTab("user")
				return nil
			}
			if event.Rune() == 'r' {
				switchTab("root")
				return nil
			}
			if event.Rune() == 'p' {
				switchTab("projects")
				return nil
			}
			if event.Key() == tcell.KeyLeft || event.Rune() == 'h' {
				if currentTab == "root" {
					switchTab("user")
				} else if currentTab == "projects" {
					switchTab("root")
				} else {
					switchTab("projects")
				}
				return nil
			}
			if event.Key() == tcell.KeyRight || event.Rune() == 'l' {
				if currentTab == "user" {
					switchTab("root")
				} else if currentTab == "root" {
					switchTab("projects")
				} else {
					switchTab("user")
				}
				return nil
			}
			if event.Rune() == 'o' {
				urlInput := tview.NewInputField().
					SetLabel(" URL: ").
					SetFieldWidth(50)

				form := tview.NewForm().
					AddFormItem(urlInput).
					AddButton("Fetch", func() {
						url := urlInput.GetText()
						if url != "" {
							loadingText := tview.NewTextView().
								SetTextAlign(tview.AlignCenter).
								SetDynamicColors(true)
							loadingText.SetBorder(true).SetTitle(" 🌐 Fetching Connection ").SetTitleColor(tcell.ColorForestGreen)

							loadingModal := tview.NewFlex().
								AddItem(nil, 0, 1, false).
								AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
									AddItem(nil, 0, 1, false).
									AddItem(loadingText, 5, 1, true).
									AddItem(nil, 0, 1, false), 50, 1, true).
								AddItem(nil, 0, 1, false)

							pages.AddPage("loadingModal", loadingModal, true, true)
							pages.ShowPage("loadingModal")
							app.SetFocus(loadingModal)

							stopAnim := make(chan bool)
							go func() {
								frames := []string{"[green]⠋[-] 📡", "[green]⠙[-] 📡", "[green]⠹[-] 📡", "[green]⠸[-] 📡", "[green]⠼[-] 📡", "[green]⠴[-] 📡", "[green]⠦[-] 📡", "[green]⠧[-] 📡", "[green]⠇[-] 📡", "[green]⠏[-] 📡"}
								i := 0
								for {
									select {
									case <-stopAnim:
										return
									default:
										app.QueueUpdateDraw(func() {
											loadingText.SetText(fmt.Sprintf("\n%s  Connecting to:\n[yellow]%s[-]", frames[i%len(frames)], url))
										})
										i++
										time.Sleep(100 * time.Millisecond)
									}
								}
							}()

							go func() {
								if strings.Contains(url, "github.com") && !strings.Contains(url, "raw.githubusercontent.com") && !strings.HasSuffix(url, ".ini") {
									projects, err := fetchGitRepo(url)
									stopAnim <- true
									app.QueueUpdateDraw(func() {
										pages.RemovePage("loadingModal")
										if err == nil {
											remoteProjects = append(remoteProjects, projects...)
											refreshInstances()
											pages.RemovePage("urlInput")
											app.SetFocus(table)
											switchTab("projects")
										} else {
											pages.RemovePage("urlInput")
											errModal := tview.NewModal().
												SetText("Error fetching Repo: " + err.Error()).
												AddButtons([]string{"OK"}).
												SetDoneFunc(func(buttonIndex int, buttonLabel string) {
													pages.RemovePage("errorModal")
													app.SetFocus(table)
												})
											pages.AddPage("errorModal", errModal, false, false)
											pages.ShowPage("errorModal")
											app.SetFocus(errModal)
										}
									})
								} else {
									fileName, content, err := fetchRemoteIni(url)
									stopAnim <- true
									app.QueueUpdateDraw(func() {
										pages.RemovePage("loadingModal")
										if err == nil {
											os.MkdirAll(filepath.Join(os.TempDir(), "dboxshim", "inis"), 0755)
											tempPath := filepath.Join(os.TempDir(), "dboxshim", "inis", fileName)
											os.WriteFile(tempPath, []byte(content), 0644)
											remoteProject := ProjectFile{
												Name:    "🌐 " + fileName,
												Path:    tempPath,
												Content: content,
												RepoName: "Remote Files",
											}
											remoteProjects = append(remoteProjects, remoteProject)
											refreshInstances()
											pages.RemovePage("urlInput")
											app.SetFocus(table)
											switchTab("projects")
										} else {
											pages.RemovePage("urlInput")
											errModal := tview.NewModal().
												SetText("Error fetching URL: " + err.Error()).
												AddButtons([]string{"OK"}).
												SetDoneFunc(func(buttonIndex int, buttonLabel string) {
													pages.RemovePage("errorModal")
													app.SetFocus(table)
												})
											pages.AddPage("errorModal", errModal, false, false)
											pages.ShowPage("errorModal")
											app.SetFocus(errModal)
										}
									})
								}
							}()
						}
					}).
					AddButton("Cancel", func() {
						pages.RemovePage("urlInput")
						app.SetFocus(table)
					})

				form.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
					if event.Key() == tcell.KeyEscape {
						pages.RemovePage("urlInput")
						app.SetFocus(table)
						return nil
					}
					return event
				})

				form.SetBorder(true).SetTitle(" Open Remote INI URL ").SetTitleColor(tcell.ColorForestGreen)

				flex := tview.NewFlex().
					AddItem(nil, 0, 1, false).
					AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
						AddItem(nil, 0, 1, false).
						AddItem(form, 7, 1, true).
						AddItem(nil, 0, 1, false), 60, 1, true).
					AddItem(nil, 0, 1, false)

				pages.AddPage("urlInput", flex, true, true)
				pages.ShowPage("urlInput")
				app.SetFocus(form)
				return nil
			}
			return event
		})

		app.SetMouseCapture(func(event *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
			// Do not overwrite right click
			if event.Buttons()&tcell.Button3 != 0 {
				return nil, 0
			}
			return event, action
		})

		app.SetMouseCapture(func(event *tcell.EventMouse, action tview.MouseAction) (*tcell.EventMouse, tview.MouseAction) {
			// Do not overwrite right click
			if event.Buttons()&tcell.Button3 != 0 {
				return nil, 0
			}
			return event, action
		})

		if err := app.SetRoot(pages, true).SetFocus(table).Run(); err != nil {

			fmt.Printf("Error starting app: %s\n", err)
			return
		}
	} // end of else block

	if selectedAction == "enter" && selectedInstance != nil {
		crumbleEffect()
		args := []string{"enter", selectedInstance.Name}
		if selectedInstance.Type == "Root" {
			args = append([]string{"--root"}, args...)
		}
		cmd := exec.Command("distrobox", args...)
		absDir, _ := filepath.Abs(selectedInstance.Name)
		if info, err := os.Stat(absDir); err == nil && info.IsDir() {
			cmd.Dir = absDir
		}
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("Error entering distrobox: %s\n", err)
		}
	} else if selectedAction == "permanent" && selectedProject != nil {
		_, name := parseIniFile(selectedProject.Path)
		exists := false
		for _, inst := range userInstances {
			if inst.Name == name {
				exists = true
				break
			}
		}
		for _, inst := range rootInstances {
			if inst.Name == name {
				exists = true
				break
			}
		}

		if exists {
			fmt.Printf("\033[31m❌ Instance '%s' already exists. Cancelling permanent creation.\033[0m\n", name)
			return
		}

		crumbleEffect()
		fmt.Printf("\033[38;2;48;186;120m🚀 Assembling permanent Distrobox instance from %s...\033[0m\n", selectedProject.Name)
		absPath, _ := filepath.Abs(selectedProject.Path)
		cmd := exec.Command("distrobox", "assemble", "create", "--replace", "--file", absPath)
		var absDir string
		if selectedProject.RepoPath != "" {
			iniDirRelative := filepath.Dir(strings.TrimPrefix(selectedProject.Path, selectedProject.RepoPath+"/"))
			if iniDirRelative != "." && iniDirRelative != "" {
				exec.Command("git", "-C", selectedProject.RepoPath, "checkout", "HEAD", "--", iniDirRelative).Run()
			}
			if name != "" {
				exec.Command("git", "-C", selectedProject.RepoPath, "checkout", "HEAD", "--", filepath.Join(iniDirRelative, name)).Run()
			}
			absDir = filepath.Join(selectedProject.RepoPath, iniDirRelative, name)
			if info, err := os.Stat(absDir); err != nil || !info.IsDir() {
				absDir = filepath.Join(selectedProject.RepoPath, iniDirRelative)
			}
		} else {
			absDir, _ = filepath.Abs(name)
		}
		if info, err := os.Stat(absDir); err == nil && info.IsDir() {
			cmd.Dir = absDir
		}
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("Error assembling instance: %s\n", err)
			return
		}
		if name != "" {
			fmt.Printf("\033[38;2;48;186;120m✅ Created successfully. Entering %s...\033[0m\n", name)
			enterCmd := exec.Command("distrobox", "enter", name)
			if info, err := os.Stat(absDir); err == nil && info.IsDir() {
				enterCmd.Dir = absDir
			}
			enterCmd.Stdin = os.Stdin
			enterCmd.Stdout = os.Stdout
			enterCmd.Stderr = os.Stderr
			enterCmd.Run()
		}
	} else if selectedAction == "ephemeral" && selectedProject != nil {
		args, name := parseIniFile(selectedProject.Path)
		if len(args) == 0 {
			fmt.Println("Error parsing INI file or no parameters found.")
			return
		}

		originalName := name
		exists := false
		var existID string
		for _, inst := range userInstances {
			if inst.Name == name {
				exists = true
				existID = inst.ID
				break
			}
		}
		if !exists {
			for _, inst := range rootInstances {
				if inst.Name == name {
					exists = true
					existID = inst.ID
					break
				}
			}
		}

		if exists {
			suffix := existID
			if suffix == "" {
				rand.Seed(time.Now().UnixNano())
				suffix = fmt.Sprintf("%04x", rand.Intn(0x10000))
			}
			newName := fmt.Sprintf("%s-%s", name, suffix)
			for i, arg := range args {
				if arg == "--name" && i+1 < len(args) {
					args[i+1] = newName
					break
				}
			}
			name = newName
		}

		crumbleEffect()
		fmt.Printf("\033[38;2;48;186;120m⚡ Launching ephemeral instance %s from %s...\033[0m\n", name, selectedProject.Name)
		fullArgs := append([]string{"--yes"}, args...)
		cmd := exec.Command("distrobox-ephemeral", fullArgs...)
		var absDir string
		if selectedProject.RepoPath != "" {
			iniDirRelative := filepath.Dir(strings.TrimPrefix(selectedProject.Path, selectedProject.RepoPath+"/"))
			if iniDirRelative != "." && iniDirRelative != "" {
				exec.Command("git", "-C", selectedProject.RepoPath, "checkout", "HEAD", "--", iniDirRelative).Run()
			}
			if originalName != "" {
				exec.Command("git", "-C", selectedProject.RepoPath, "checkout", "HEAD", "--", filepath.Join(iniDirRelative, originalName)).Run()
			}
			absDir = filepath.Join(selectedProject.RepoPath, iniDirRelative, originalName)
			if info, err := os.Stat(absDir); err != nil || !info.IsDir() {
				absDir = filepath.Join(selectedProject.RepoPath, iniDirRelative)
			}
		} else {
			absDir, _ = filepath.Abs(originalName)
		}
		if info, err := os.Stat(absDir); err == nil && info.IsDir() {
			cmd.Dir = absDir
		}
		cmd.Stdin = os.Stdin
		cmd.Stdout = os.Stdout
		cmd.Stderr = os.Stderr
		if err := cmd.Run(); err != nil {
			fmt.Printf("Error running ephemeral instance: %s\n", err)
		}
	}
}

func crumbleEffect() {
	width, height, err := term.GetSize(int(os.Stdout.Fd()))
	if err != nil {
		width = 80
		height = 24
	}
	fmt.Print("\033[?25l")
	defer fmt.Print("\033[?25h")
	blocks := []string{"█", "▓", "▒", "░", " "}
	colors := []string{
		"\033[38;2;168;230;197m",
		"\033[38;2;122;208;158m",
		"\033[38;2;76;197;139m",
		"\033[38;2;48;186;120m",
	}
	maxDist := width + height*2
	steps := 15
	stepSize := maxDist / steps
	if stepSize < 1 {
		stepSize = 1
	}
	for t := 0; t <= maxDist+stepSize*3; t += stepSize {
		var sb strings.Builder
		sb.WriteString("\033[H")
		for r := 0; r < height; r++ {
			skipCount := 0
			for c := 0; c < width; c++ {
				dist := c + r*2
				if dist < t-stepSize*3 {
					if skipCount > 0 {
						sb.WriteString(fmt.Sprintf("\033[%dC", skipCount))
						skipCount = 0
					}
					sb.WriteString(" ")
				} else if dist < t {
					if skipCount > 0 {
						sb.WriteString(fmt.Sprintf("\033[%dC", skipCount))
						skipCount = 0
					}
					color := colors[rand.Intn(len(colors))]
					block := blocks[rand.Intn(len(blocks))]
					if block == " " {
						sb.WriteString(" ")
					} else {
						sb.WriteString(color + block + "\033[0m")
					}
				} else {
					skipCount++
				}
			}
			if r < height-1 {
				sb.WriteString("\r\n")
			}
		}
		fmt.Print(sb.String())
		time.Sleep(40 * time.Millisecond)
	}
	fmt.Print("\033[2J\033[H")
}
