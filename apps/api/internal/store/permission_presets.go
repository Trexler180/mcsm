package store

// The per-server permission vocabulary and its named presets, as data. The
// dashboard draws its access editor and "view as" roles from this, so the UI
// and the API share one definition instead of two that drift.

// PermissionGroup is one group of the access editor.
type PermissionGroup struct {
	Group  string           `json:"group"`
	Label  string           `json:"label"`
	Detail string           `json:"detail"`
	Leaves []PermissionLeaf `json:"leaves"`
}

// PermissionLeaf is one fine-grained permission inside a group.
type PermissionLeaf struct {
	Permission string `json:"permission"`
	Label      string `json:"label"`
}

// PermissionPreset is a named set of per-server permissions.
type PermissionPreset struct {
	ID          string   `json:"id"`
	Label       string   `json:"label"`
	Description string   `json:"description"`
	Permissions []string `json:"permissions"`
}

// PermissionModel lists every group and leaf, in display order.
func PermissionModel() []PermissionGroup {
	leaf := func(p ServerPermission, label string) PermissionLeaf {
		return PermissionLeaf{Permission: string(p), Label: label}
	}
	return []PermissionGroup{
		{Group: string(ServerPermissionView), Label: "View", Detail: "Open the overview, status and stats", Leaves: []PermissionLeaf{}},
		{Group: string(ServerPermissionPower), Label: "Power", Detail: "Control the server lifecycle", Leaves: []PermissionLeaf{
			leaf(ServerPermissionPowerStart, "Start"), leaf(ServerPermissionPowerStop, "Stop"),
			leaf(ServerPermissionPowerRestart, "Restart"), leaf(ServerPermissionPowerKill, "Kill"),
		}},
		{Group: string(ServerPermissionConsole), Label: "Console", Detail: "Read the console and send commands", Leaves: []PermissionLeaf{}},
		{Group: string(ServerPermissionPlayers), Label: "Players", Detail: "Manage who plays here", Leaves: []PermissionLeaf{
			leaf(ServerPermissionPlayersWhitelist, "Whitelist"), leaf(ServerPermissionPlayersKick, "Kick"),
			leaf(ServerPermissionPlayersBan, "Ban and pardon"), leaf(ServerPermissionPlayersOp, "Op and deop"),
			leaf(ServerPermissionPlayersInspect, "View saved data"), leaf(ServerPermissionPlayersDelete, "Delete saved data"),
		}},
		{Group: string(ServerPermissionFiles), Label: "Files", Detail: "Server files, logs and worlds", Leaves: []PermissionLeaf{
			leaf(ServerPermissionFilesRead, "Read and download"), leaf(ServerPermissionFilesWrite, "Edit and upload"),
			leaf(ServerPermissionFilesDelete, "Delete"),
		}},
		{Group: string(ServerPermissionMods), Label: "Mods", Detail: "Install and manage content", Leaves: []PermissionLeaf{
			leaf(ServerPermissionModsInstall, "Install"), leaf(ServerPermissionModsUpdate, "Update"),
			leaf(ServerPermissionModsRemove, "Remove"),
		}},
		{Group: string(ServerPermissionBackups), Label: "Backups", Detail: "Create and restore backups", Leaves: []PermissionLeaf{
			leaf(ServerPermissionBackupsCreate, "Create"), leaf(ServerPermissionBackupsRestore, "Restore"),
			leaf(ServerPermissionBackupsDelete, "Delete"),
		}},
		{Group: string(ServerPermissionTasks), Label: "Schedules", Detail: "Create and edit scheduled tasks", Leaves: []PermissionLeaf{}},
		{Group: string(ServerPermissionSettings), Label: "Settings", Detail: "Software, version and server.properties", Leaves: []PermissionLeaf{}},
		{Group: string(ServerPermissionAdmin), Label: "Access", Detail: "Manage who can access this server", Leaves: []PermissionLeaf{}},
	}
}

// PermissionPresets are the named roles a member can be given in one step.
func PermissionPresets() []PermissionPreset {
	ps := func(perms ...ServerPermission) []string {
		out := make([]string, len(perms))
		for i, p := range perms {
			out[i] = string(p)
		}
		return out
	}
	return []PermissionPreset{
		{
			ID: "administrator", Label: "Administrator",
			Description: "Everything on this server, including who else can access it.",
			Permissions: AllServerPermissions(),
		},
		{
			ID: "operator", Label: "Operator",
			Description: "Runs the server day to day: power (not kill), console, moderation, files, mods, backups and schedules. Cannot restore or delete backups, change settings, or manage access.",
			Permissions: ps(ServerPermissionView,
				ServerPermissionPowerStart, ServerPermissionPowerStop, ServerPermissionPowerRestart,
				ServerPermissionConsole,
				ServerPermissionPlayersWhitelist, ServerPermissionPlayersKick, ServerPermissionPlayersBan, ServerPermissionPlayersInspect,
				ServerPermissionFilesRead, ServerPermissionFilesWrite,
				ServerPermissionModsInstall, ServerPermissionModsUpdate,
				ServerPermissionBackupsCreate,
				ServerPermissionTasks),
		},
		{
			ID: "viewer", Label: "Viewer",
			Description: "Can see the server's status and stats, and nothing else.",
			Permissions: ps(ServerPermissionView),
		},
	}
}

// PermissionPresetByID returns a preset's permissions.
func PermissionPresetByID(id string) ([]string, bool) {
	for _, p := range PermissionPresets() {
		if p.ID == id {
			return append([]string(nil), p.Permissions...), true
		}
	}
	return nil, false
}
