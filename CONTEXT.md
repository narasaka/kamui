# Kamui

Kamui gives local clients access to eligible TCP services listening on a remote
SSH host's loopback interface.

## Language

**Excluded port**:
A discovered remote TCP port that the effective port policy omits from
mirroring. It is neither a mirrored port nor a conflict.
_Avoid_: Ignored port, blocked port

**Conflict**:
An eligible remote TCP port whose corresponding local loopback port Kamui could
not claim.
_Avoid_: Excluded port
