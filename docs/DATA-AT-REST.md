# Protecting data at rest on the host

When the editor runs on the host (the unified launch — `pbcssg server -admin-addr`,
SPEC §7.9), writable data lives on the server: the authoring database `content.db` and
the runtime store `app.db` (accounts, WebAuthn credentials, sessions, single-use invites,
comments — SPEC §2.4). This note describes how to protect that data at rest
**generically** — no assumptions about a specific hosting provider.

The **public bundle is not sensitive** — it is exactly what you publish — so this is
only about the *writable* data directory, not the served site.

## Threat model

The concern is not a stranger with physical access to a disk. It is a **host disk image
leaking**: a virtual machine snapshot or a backup image that is restored elsewhere or
exposed in your own hosting account. Managed "encryption at rest" offered by a host
usually uses **the host's keys**, which does nothing against a snapshot that ends up in
the wrong hands — the restored image is readable. Protecting against that requires a key
**you** hold and that is never written to the disk being snapshotted.

## The approach: a sealed, key-in-RAM volume

Put the writable data directory (e.g. `/srv/pbcssg/data`, holding `content.db` and
`app.db`) on an **encrypted volume whose key you supply at unlock time and keep only in
memory**:

- The key (a passphrase or keyfile) lives in your head or a local password manager. You
  enter it over SSH when you unlock the volume, and it stays in RAM.
- **Never** put the passphrase or a keyfile on the unencrypted root filesystem (e.g. in
  `/etc/crypttab`) — a host snapshot would capture it and defeat the whole thing. That is
  why the volume is unlocked **manually over SSH**, not automatically at boot.
- Encryption is at the **volume layer**, so the application stays unchanged (no app-level
  crypto, no cgo).

Result: host snapshots and backup images contain only **ciphertext** for the data dir.

### On a host with dm-crypt (LUKS) — most virtual servers

A LUKS **container file** on the existing disk (no separate volume needed), or an
attached block volume you `luksFormat`. One-time setup:

```bash
# 512 MB container for the writable data dir (grow to taste)
fallocate -l 512M /srv/pbcssg/data.img
cryptsetup luksFormat /srv/pbcssg/data.img            # sets the passphrase
cryptsetup luksOpen   /srv/pbcssg/data.img pbcssg-data
mkfs.ext4 /dev/mapper/pbcssg-data
mkdir -p /srv/pbcssg/data
mount /dev/mapper/pbcssg-data /srv/pbcssg/data
```

On each boot, unlock over SSH, then start the service:

```bash
cryptsetup luksOpen /srv/pbcssg/data.img pbcssg-data  # prompts; passphrase stays in RAM
mount /dev/mapper/pbcssg-data /srv/pbcssg/data
systemctl start pbcssg
```

### Without dm-crypt (userspace fallback)

If the host cannot use dm-crypt but has FUSE, [gocryptfs](https://github.com/rfjakob/gocryptfs)
encrypts a directory in userspace:

```bash
gocryptfs -init /srv/pbcssg/data.cipher               # sets the password
gocryptfs        /srv/pbcssg/data.cipher /srv/pbcssg/data   # unlock over SSH; password in RAM
```

### If the host offers whole-disk / whole-volume encryption with *your* key

That is the simplest equivalent — use it instead of a container. The requirement is the
same: **you** hold the key and it is not sitting in plaintext on the snapshotted disk.
Host-managed encryption with the host's keys does **not** satisfy this.

## Graceful degradation

The **public serving path never opens the data directory** — it serves the immutable
bundle (SPEC §7.1). So a sealed volume degrades the *private* surface, never the public
one, and `pbcssg server` is built to match: if `-app-db` (or the admin's `-db`) cannot be
opened at startup — the normal state of a sealed volume that has not been unlocked yet —
the process **does not exit**. It logs a loud `WARN` and serves the static bundle anyway,
with the dynamic layer (comments + member/creator passkey auth) and the admin editor
disabled until the volume is unlocked and the server is restarted:

```
WARN … cannot open -app-db "…/app.db": … (14)
WARN … serving the static bundle only — comments and passkey auth are DISABLED until app.db is available and the server is restarted (§7.9)
WARN … admin editor + metrics DISABLED (its content.db is unavailable — e.g. a sealed volume, §7.9); the public site is unaffected
```

So a reboot brings the **public site up immediately**, even in a single unified unit; you
then unlock the volume over SSH and restart the service to re-enable comments and the
editor. (The loud `WARN` is deliberate so a genuine misconfiguration — a wrong path — is
not silently mistaken for a sealed volume.)

Boot → unlock → restart, on a host with dm-crypt:

```bash
# 1. The service is already up serving the static bundle (dynamic features degraded).
cryptsetup luksOpen /srv/pbcssg/data.img pbcssg-data   # prompts; passphrase stays in RAM
mount /dev/mapper/pbcssg-data /srv/pbcssg/data
systemctl restart pbcssg                               # now app.db/content.db open → full features
```

If you run the editor in the same `systemd` unit as the public server, that unit needs
write access to `/srv/pbcssg` (add `ReadWritePaths=/srv/pbcssg` to the hardened unit in
the README). You may let it start before the volume is mounted (it degrades gracefully as
above and you `systemctl restart` after unlocking), or gate it on the mount with
`RequiresMountsFor=/srv/pbcssg/data` if you prefer it to wait.

## Two more habits

- **Disable or encrypt swap.** If the host swaps to the unencrypted root, your in-RAM key
  or decrypted data could be paged to disk and captured in a snapshot. Simplest:
  `swapoff -a` on this host (or use encrypted swap).
- **Keep backup retention short.** A "forget me" hard-delete removes a row from the live
  database immediately, but an old backup image still contains it. Short retention lets
  deleted data age out within the backup cycle. With the volume encrypted, those backups
  are ciphertext regardless.

## What this does *not* need

Because of the identity model (SPEC §2.4) the data dir holds **no secrets** — no
passwords, no password hashes, no private keys (WebAuthn private keys never leave the
authenticator), and session tokens are stored hashed. Most of `app.db` (approved comments
and their aliases) is public by design. So the goal here is protecting **pseudonymous
personal data** if an image leaks, not guarding a secret vault — volume encryption plus
the habits above is proportionate, and app-level column encryption is unnecessary.
