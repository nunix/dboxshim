# DboxShim

DboxShim is an advanced, lightweight terminal user interface (TUI) and CLI tool for managing Distrobox instances, written in Go (AI only). It seamlessly handles container lifecycles, project manifests (`.ini`), and system statistics without leaving the terminal.

## Features

- **Modern Split-Pane TUI**: Built with `tview`, featuring an elegant multi-pane layout (List, Details, and Commands).
- **Multi-Tab Navigation**: Easily switch between User Instances, Root Instances, and Local Projects using `u`, `r`, `p`, or carousel mode (`←`/`→` or `h`/`l`).
- **Interactive Container Management**:
  - **Start/Stop**: Toggle container states instantly with `Space`.
  - **Delete**: Safely delete instances using `d` (includes an interactive confirmation modal).
  - **Enter**: Connect to a running container instantly with `Enter`. Features a visually stunning "diagonal drop" ANSI transition animation as you seamlessly drop into the shell.
- **Export Display**: Natively shows exported applications and binaries associated with each container.
- **Project Workflows (INI Manifests)**:
  - Scans your working directory for `distrobox-assemble` compatible `*.ini` manifests.
  - Interactively build **Permanent** or **Ephemeral** instances directly from the TUI.
  - Ephemeral containers automatically inherit the current project directory and delete themselves upon exit.
- **Live Hardware Stats**: The bottom status bar provides real-time CPU, RAM (via `podman stats`), and GPU metrics (via `rocm-smi`/`nvidia-smi`) without blocking the UI.
- **CLI Mode**: Bypass the TUI entirely to execute project logic:
  - `dboxshim list -p <project.ini>`: Launch a permanent project instance.
  - `dboxshim list -p <project.ini> -e`: Launch an ephemeral project instance.

## Installation

Ensure you have Go installed, then clone and build:

```bash
git clone <repository-url>
cd dboxshim
go build -o dboxshim
mv dboxshim ~/.local/bin/
```

## Usage

Simply run `dboxshim` to launch the interactive TUI. 
Navigate with arrow keys (`↑`/`↓`) or `j`/`k`. 

```bash
# Launch TUI
dboxshim

# Run a project from an INI file (Permanent)
dboxshim list -p myproject.ini

# Run a project from an INI file (Ephemeral)
dboxshim list -p myproject.ini -e
```

## Dependencies
- `distrobox`
- `podman` or `docker`
- `golang` (for building)
