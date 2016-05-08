# iOS Push Provider for Courier IMAP
Courier-APNs is a small daemon designed to add Push support to Courier IMAP. Even tough 
the implementation is kept simplistic it was designed to be scalable. 

## Features
 - Uses APNS/2 and hence supports HTTP/2 connection
 - Uses persistent connections to APNs
 - Manages expired tokens
 - Integration into Courier IMAP has a minimal risk

## Caveats
I have Courier-APNs running for several domains without any issues. However my systems
handles a couple of hundred emails per day only. The code is not tested in high load situations.
Moreover adding Push support to Courier IMAP requires changes to the source code.

_Note:_ Courier-APNs assumes that all mail accounts share a single UID/GID i.e. mail:mail.

## Prerequisites
 - Google Go
 - daemonize
 - maildrop
 - openbsd-netcat or socat

## Install 
- Obtain a valid Certificate for the Apple Push Notifications Service.
  Stefan Arentz has a well written [guide](https://github.com/st3fan/dovecot-xaps-daemon).
  If you don’t own an OS X Server you can try to sign up for a Certificate on 
  [Apples Push Certificates Portal](https://identity.apple.com).
- Install Courier-APNs
```sh
go get -u github.com/drandreas/courier-apns
```
- Launch Courier-APNs. It will display your UID. Keep it for later.
```
$GOPATH/bin/courier-apns /path/to/certificate /path/to/private/key
2016/05/06 19:39:50 Loading certificate...
2016/05/06 19:39:50 UID=com.apple.mail.XServer.647cc087-774a-41ab-b9e5-77fafef13b8e
2016/05/06 19:39:50 Make sure your IMAP_XAPPLEPUSHSERVICE_TOPIC is set accordingly.
2016/05/06 19:39:50 Establishing connection...
2016/05/06 19:39:50 Waiting for request...
```
- Stop Courier-APNs `CTRL-C` and re-launch it as daemon:
```sh
daemonize -p /var/run/courier/courierapns.pid  \
          -l /var/run/courier/courierapns.lock \
          -u mail  \
          $GOPATH/bin/courier-apns -d \
                     /path/to/certificate \
                     /path/to/private/key
```
- Patch Courier IMAP with `courier-0.75.0-imap.patch`, compile and install it.
- Add `XAPPLEPUSHSERVICE` to `IMAP_CAPABILITY` in `/etc/courier/imapd`
- Add `IMAP_XAPPLEPUSHSERVICE_TOPIC=YOUR_UID` to `/etc/courier/imapd`
- Resetart Courier IMAP and check if it is advertising `XAPPLEPUSHSERVICE`
```sh
telnet localhost 143
Trying ::1...
Connected to localhost.
Escape character is '^]'.
* OK [CAPABILITY ... XAPPLEPUSHSERVICE ...] Courier-IMAP ready.
```
- Add push to your mail delivery routine `/etc/courier/maildroprc`

_with netcat:_
```
# Push Notification...
PUSH=`echo $HOME | nc.openbsd -U /var/run/courier/courierapns.socket`
echo $PUSH
```

_with socat:_
```
# Push Notification...
PUSH=`echo $HOME | socat /var/run/courier/courierapns.socket STDIN`
# Note: echo $PUSH will not work with socat
```
- Reboot your iDevice and manually refresh your Inbox. Push support should now be available in Settings.
  In rare cases you might need to re-add your mail account to your iDevice for it to recognize push support.

## Debugging
- Courier-APNs is verbose. If you run into issues check `/var/log/syslog` and `/var/log/mail.log` for hints.
- Courier-APNs stores its data in `$Maildir/.push/`. For each device a separate file is created e.g.
```
{
   "aps-version": 2,
   "aps-account-id": "0715A26B-CA09-4730-A419-793000CA982E", 
   "aps-device-token": "2918390218931890821908309283098109381029309829018310983092892829", 
   "mailboxes": [ "INBOX", 
                  "INBOX.Sent"
                ]
}
```

## Command line
```
courier-apns --help
usage: courier-apns [<flags>] <crt> <key>

Listens to on a Unix socket and performs mail notifications.

This deamon supports persistent connections to APNs. The expected input on the Unix socket
is a single line containing a path to a Maildir. The client can be as simple as a piped echo
e.g. echo /path/to/maildir | nc.openbsd -U /var/run/courier/courierapns.socket.
Flags:
      --help        Show context-sensitive help (also try --help-long and --help-man).
  -s, --socket="/var/run/courier/courierapns.socket"
                    Path to use for Unix socket.
  -d, --syslog      Use Syslog instead of STDERR.
  -c, --concurrent  Enable concurrent request handling.
      --version     Show application version.

Args:
  <crt>  Path to certificate file.
  <key>  Path to private key file.
```