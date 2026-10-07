// Local UI state for Dice Arena, written as HyperChi hc-* components. Game
// state never lives here: the server renders it and pushes it over the socket.

// arena: page-wide state on <body>. Connection status, the invite button,
// and keyboard shortcuts for the dice tray.
HyperChi.registerComponent("arena", () => ({
  online: true,
  copyLabel: "Copy invite link",
  copyStatus: "",

  async copyInvite(event) {
    const url = `${location.origin}/r/${event.currentTarget.dataset.code}`;
    try {
      await navigator.clipboard.writeText(url);
      this.copyLabel = "Link copied";
      this.copyStatus = "Invite link copied.";
    } catch {
      this.copyLabel = url;
      this.copyStatus = `Copy this link: ${url}`;
    }
    setTimeout(() => {
      this.copyLabel = "Copy invite link";
    }, 2500);
  },

  // 1 to 6 toggle the dice on the table, R rolls, B banks. Shortcuts only act
  // on controls that are enabled, so they can never do more than a click can.
  shortcut(event) {
    if (event.altKey || event.ctrlKey || event.metaKey) return;
    if (event.target.closest("input:not([type=checkbox]), textarea, select, [contenteditable]")) return;
    const tray = document.querySelector("#table .tray");
    if (!tray) return;
    const key = event.key.toLowerCase();
    let control = null;
    if (key >= "1" && key <= "6") {
      control = tray.querySelectorAll('input[name="keep"]')[Number(key) - 1];
    } else if (key === "r" || key === "b") {
      control = tray.querySelector(`button[data-key="${key}"]`);
    }
    if (control && !control.disabled && !control.closest("fieldset:disabled")) {
      event.preventDefault();
      control.click();
    }
  },
}));

// tray: how many dice are picked, so the set-aside buttons enable only when
// the selection is not empty. The server still checks that they score.
HyperChi.registerComponent("tray", () => ({
  picked: 0,
  count(event) {
    this.picked = event.currentTarget.querySelectorAll('input[name="keep"]:checked').length;
  },
}));

// nameEditor: the "Change name" disclosure in the lobby.
HyperChi.registerComponent("nameEditor", (editing) => ({ editing: !!editing }));

// dismissable: a flash message the player can close.
HyperChi.registerComponent("dismissable", () => ({ open: true }));

HyperChi.start();
