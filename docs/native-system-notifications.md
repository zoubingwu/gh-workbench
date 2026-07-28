# Native system notifications

Status: implemented for macOS

## Design

GitHub Workbench detects notification-worthy snapshot changes in Go and sends
informational macOS notifications through `/usr/bin/osascript`. The browser UI
owns account-scoped preferences, and SQLite persists them across process
restarts.

The existing coalesced publication path performs the notification work:

1. Load the current snapshot.
2. Publish it to WebSocket clients.
3. Advance the in-memory notification cursor.
4. Send notifications for new relevant items and newer activity.

The cursor seeds silently after the first successful sync, advances while
notifications are disabled, preserves activity timestamps across transient
search omissions, and ignores activity created by the active viewer.

`internal/notification` owns change detection and delivery. `internal/app`
composes the sender and observes snapshots from the scheduler publication path.
WebSocket connection snapshots remain outside notification delivery.

## Preferences

The browser reads and updates two account-scoped SQLite preferences through a
same-origin API:

- `notificationsEnabled`
- `onlyMyPullRequests`

The Go process must remain running for delivery. Sender failures produce one
session warning while synchronization continues.

## macOS sender

The sender executes a fixed AppleScript and passes the title and body as
separate process arguments:

```applescript
on run argv
    display notification (item 2 of argv) with title (item 1 of argv)
end run
```

This preserves the existing raw executable and `CGO_ENABLED=0` release model.
Notifications carry scripting-host attribution and provide informational
delivery.

A signed helper application is the path for stable application attribution,
notification settings, sound, and GitHub URL activation. That packaging work
requires a bundle identity, signing, and notarization.

## Primary sources

- [Apple: Asking permission to use notifications](https://developer.apple.com/documentation/usernotifications/asking-permission-to-use-notifications)
- [Apple: Handling notifications and notification-related actions](https://developer.apple.com/documentation/usernotifications/handling-notifications-and-notification-related-actions)
- [Apple: Creating distribution-signed code for the Mac](https://developer.apple.com/documentation/xcode/creating-distribution-signed-code-for-the-mac/)
- [GitHub CLI: Creating precompiled extensions](https://docs.github.com/en/github-cli/github-cli/creating-github-cli-extensions)
