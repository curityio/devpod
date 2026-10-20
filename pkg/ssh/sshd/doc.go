/*
Package sshd is devpod's copy of github.com/gliderlabs/ssh v0.3.8, with unix socket
forwarding (direct-streamlocal@openssh.com and streamlocal-forward@openssh.com) added and
Session.Command removed (use Session.RawCommand). See LICENSE for the original copyright.

Package sshd wraps the crypto/ssh package with a higher-level API for building
SSH servers. The goal of the API was to make it as simple as using net/http, so
the API is very similar.

You should be able to build any SSH server using only this package, which wraps
relevant types and some functions from crypto/ssh. However, you still need to
use crypto/ssh for building SSH clients.

ListenAndServe starts an SSH server with a given address, handler, and options. The
handler is usually nil, which means to use DefaultHandler. Handle sets DefaultHandler:

	sshd.Handle(func(s sshd.Session) {
	    io.WriteString(s, "Hello world\n")
	})

	log.Fatal(sshd.ListenAndServe(":2222", nil))

If you don't specify a host key, it will generate one every time. This is convenient
except you'll have to deal with clients being confused that the host key is different.
It's a better idea to generate or point to an existing key on your system:

	log.Fatal(sshd.ListenAndServe(":2222", nil, sshd.HostKeyFile("/Users/progrium/.ssh/id_rsa")))

Although all options have functional option helpers, another way to control the
server's behavior is by creating a custom Server:

	s := &sshd.Server{
	    Addr:             ":2222",
	    Handler:          sessionHandler,
	    PublicKeyHandler: authHandler,
	}
	s.AddHostKey(hostKeySigner)

	log.Fatal(s.ListenAndServe())

This package automatically handles basic SSH requests like setting environment
variables, requesting PTY, and changing window size. These requests are
processed, responded to, and any relevant state is updated. This state is then
exposed to you via the Session interface.

The one big feature missing from the Session abstraction is signals. This was
started, but not completed. Pull Requests welcome!
*/
package sshd
