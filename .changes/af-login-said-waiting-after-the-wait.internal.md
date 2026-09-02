# fixed

`af login` printed "Waiting for approval..." and then nothing until it printed
success, so it kept claiming to be waiting through the identity call and the
write to the credential store. On macOS that write can now put a keychain unlock
prompt in front of somebody, so the screen was saying it was waiting for a
browser while the operating system was waiting for them. It says the approval
landed before it starts storing.
