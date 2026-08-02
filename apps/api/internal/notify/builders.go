package notify

import (
	"fmt"
	"strings"
)

// These constructors keep alert copy in one place and give detection points a
// one-liner to emit. Pass the human-friendly server/node name when available;
// it only affects display text, not matching.

func ServerCrash(serverID, serverName string) Event {
	return Event{
		Type: EventServerCrash, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("%s crashed", display(serverName, serverID)),
		Body:  "The server went offline unexpectedly with no panel-initiated stop. It may have crashed.",
	}
}

func ServerStartFailed(serverID, serverName, reason string) Event {
	body := "A start attempt did not reach the online state."
	if reason != "" {
		body = reason
	}
	return Event{
		Type: EventServerStartFailed, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("%s failed to start", display(serverName, serverID)), Body: body,
	}
}

func ServerOffline(serverID, serverName string) Event {
	return Event{
		Type: EventServerOffline, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("%s stopped", display(serverName, serverID)),
		Body:  "The server is now offline.",
	}
}

func ServerOnline(serverID, serverName string) Event {
	return Event{
		Type: EventServerOnline, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("%s is online", display(serverName, serverID)),
		Body:  "The server came online.",
	}
}

func ModConflict(serverID, serverName, summary string) Event {
	return Event{
		Type: EventModConflict, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("Mod conflict on %s", display(serverName, serverID)),
		Body:  summary,
		Data:  map[string]any{"summary": summary},
	}
}

func ModUpdateApplied(serverID, serverName string, count int) Event {
	return Event{
		Type: EventModUpdateApplied, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("Mods updated on %s", display(serverName, serverID)),
		Body:  fmt.Sprintf("%d mod(s) were updated.", count),
		Data:  map[string]any{"count": count},
	}
}

func ModUpdateFailed(serverID, serverName, reason string) Event {
	return Event{
		Type: EventModUpdateFailed, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("Mod update failed on %s", display(serverName, serverID)),
		Body:  reason,
	}
}

func BackupSuccess(serverID, serverName string) Event {
	return Event{
		Type: EventBackupSuccess, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("Backup completed for %s", display(serverName, serverID)),
		Body:  "A backup finished successfully.",
	}
}

func BackupFailed(serverID, serverName, reason string) Event {
	return Event{
		Type: EventBackupFailed, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("Backup failed for %s", display(serverName, serverID)),
		Body:  reason,
	}
}

func NodeOffline(nodeID, nodeName string) Event {
	return Event{
		Type: EventNodeOffline, NodeID: nodeID, NodeName: nodeName,
		Title: fmt.Sprintf("Node %s is offline", display(nodeName, nodeID)),
		Body:  "The agent node stopped responding to heartbeats.",
	}
}

// NodeDiskLow reports that a backup left the node's disk nearly full. detail
// carries the agent's measurement (e.g. "4.2 GB free (95% used)").
func NodeDiskLow(nodeID, nodeName, detail string) Event {
	return Event{
		Type: EventNodeDiskLow, NodeID: nodeID, NodeName: nodeName,
		Title: fmt.Sprintf("Disk space low on node %s", display(nodeName, nodeID)),
		Body:  detail,
	}
}

func NodeOnline(nodeID, nodeName string) Event {
	return Event{
		Type: EventNodeOnline, NodeID: nodeID, NodeName: nodeName,
		Title: fmt.Sprintf("Node %s is online", display(nodeName, nodeID)),
		Body:  "The agent node is responding again.",
	}
}

// ServerPerformance reports sustained high resource usage. detail names the
// resource and the observed level (e.g. "CPU at 96% for 10 minutes").
func ServerPerformance(serverID, serverName, detail string) Event {
	return Event{
		Type: EventServerPerformance, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("%s is under sustained load", display(serverName, serverID)),
		Body:  detail,
	}
}

// PlayerJoinDenied reports someone the whitelist turned away. The Data payload
// is what makes the alert actionable rather than merely informative: the panel
// renders a "let them in" button straight from these fields, so the operator
// never has to go find the player in a roster the player was never added to.
func PlayerJoinDenied(serverID, serverName, player, uuid string, bedrock bool, attempts int) Event {
	body := fmt.Sprintf("%s is not on the whitelist and could not join.", player)
	if attempts > 1 {
		body = fmt.Sprintf("%s is not on the whitelist and has tried to join %d times.", player, attempts)
	}
	if bedrock {
		body += " They are connecting from Bedrock Edition."
	}
	return Event{
		Type: EventPlayerJoinDenied, ServerID: serverID, ServerName: serverName,
		Title: fmt.Sprintf("%s tried to join %s", player, display(serverName, serverID)),
		Body:  body,
		Data: map[string]any{
			"player":   player,
			"uuid":     uuid,
			"bedrock":  bedrock,
			"attempts": attempts,
		},
		// Keyed on the player, not just the server, so two different people
		// knocking inside the cooldown produce two alerts — collapsing them would
		// silently lose one of them.
		DedupeKey: EventPlayerJoinDenied + ":" + serverID + ":" + strings.ToLower(player),
	}
}

func display(name, id string) string {
	if name != "" {
		return name
	}
	return id
}
