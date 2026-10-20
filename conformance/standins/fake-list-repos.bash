# Stand-in for the issues moxin's list_repos (see ./default.nix). Like the
# real tool it reports failure as an isError result with exit 0, its
# structuredContent has per repo `mirror` and (when the tracked repository is
# listed) `tracked_at`, plus `unlisted` and `complete`, and in record mode
# (ISSUES_MODE not `live`) it appends its result as one line to
# ISSUES_TRAFFIC_FILE, created 0600.
set -euo pipefail
printf 'list_repos mode=%s issues_mode=%s\n' "${PEBBLE_TARGET_MODE:-unset}" "${ISSUES_MODE:-unset}" >> "${FAKE_LOG:?}/calls.log"
printf '%s mint=%s/%s pw=%s res=%s token=%s\n' "list_repos" "${TROUPE_MINT_USER:-}" "${TROUPE_MINT_PASSWORD_FILE:-}" \
  "${TROUPE_XMPP_PASSWORD:-}" "${TROUPE_XMPP_RESOURCE:-}" "${ISSUES_COMMIT_TOKEN:-}" >> "$FAKE_LOG/env.log"
case ",${FAKE_SCENARIO:?}," in
  # listrepos-slow: takes 2s after logging its call (for a signal delivered
  # while the glue waits on the listing).
  *,listrepos-slow,*) sleep 2 ;;
esac
case ",${FAKE_SCENARIO:?}," in
  *,repos-fail,*)
    jq -n '{content:[{type:"text",text:"list_repos: the cutting-garden origin did not answer; nothing was written"}],isError:true}'
    exit 0
    ;;
esac
result='{repos:[{repo:"linenisgreat/moxy",uri:"smith://f/linenisgreat/moxy",mirror:false},
                {repo:"amarbel-llc/moxy",uri:"smith://g/amarbel-llc/moxy",mirror:true,tracked_at:"linenisgreat/moxy"},
                {repo:"amarbel-llc/circus",uri:"smith://g/amarbel-llc/circus",mirror:false},
                {repo:"friedenberg/dotfiles",uri:"smith://h/friedenberg/dotfiles",mirror:false,private:true}],
         unlisted:[],complete:true}'
if [ "${ISSUES_MODE:-}" != live ]; then
  traffic="${ISSUES_TRAFFIC_FILE:?}"
  ( umask 077; : >> "$traffic" )
  jq -nc "{call:\"list_repos\", result:$result}" >> "$traffic"
fi
jq -n "{content:[{type:\"text\",text:\"x\"}],isError:false,structuredContent:$result}"
