# Shape-preserving normalisation of one recorded case's text. Copied verbatim as
# NORMALISE beside the captured cases, so the rules travel with the data.
#
# Run as: jq -Rsj --arg all "$ALL" --arg root "$WORLD" -f normalise.jq FILE
# where FILE is slurped as one raw string (-Rs, written with -j so no byte is
# added or lost), $ALL is the concatenation of the case's
# args, stdout and stderr (so one id gets ONE placeholder across the files of a
# case) and $root is the world directory.
#
# Rules, in order:
#   1. $root/state -> /state, $root/fake -> /fake, any other $root -> /world
#   2. ids, numbered N = 1, 2, ... by first appearance in $ALL, per class:
#        uuid                      -> 00000000-0000-4000-8000-<N, 12 digits>
#        juggler-agent-<8 hex>     -> juggler-agent-<N, 8 digits>
#        juggler-run-<8 hex>       -> juggler-run-<N, 8 digits>   (own counter)
#        chat-<8 hex>              -> chat-<N, 8 digits>
# Nothing else is touched, so the shape of every value (a JID still contains
# "@", a stanza id still starts "chat-") survives.

def uuidre: "[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}";
def jobre:  "juggler-(?:agent|run)-[0-9a-f]{8}";
def chatre: "chat-[0-9a-f]{8}";
def idre:   "(?<id>" + uuidre + "|" + jobre + "|" + chatre + ")";

def scrub($root):
  split($root + "/state") | join("/state")
  | split($root + "/fake") | join("/fake")
  | split($root) | join("/world");

def pad($n; $w): ("0" * $w + ($n | tostring))[-$w:];

# class key: the counter an id belongs to.
def cls:
  if startswith("chat-") then "chat"
  elif startswith("juggler-agent-") then "agent"
  elif startswith("juggler-run-") then "run"
  else "uuid" end;

def placeholder($c; $n):
  if $c == "chat" then "chat-" + pad($n; 8)
  elif $c == "agent" then "juggler-agent-" + pad($n; 8)
  elif $c == "run" then "juggler-run-" + pad($n; 8)
  else "00000000-0000-4000-8000-" + pad($n; 12) end;

. as $text
| ($all | scrub($root)) as $scan
| ([$scan | match(idre; "g") | .string]
   | reduce .[] as $x ([]; if index([$x]) != null then . else . + [$x] end)) as $ids
| (reduce $ids[] as $x ({counts: {}, map: {}};
     ($x | cls) as $c
     | .counts[$c] = ((.counts[$c] // 0) + 1)
     | .map[$x] = placeholder($c; .counts[$c]))
   | .map) as $map
| $text | scrub($root) | gsub(idre; $map[.id])
