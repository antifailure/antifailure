# fixed

Every refusal in the scheduled Sign-up probe named `000000` when it could not
reach the origin at all, and no server can send that.

`curl -w '%{http_code}'` writes `000` for a connection that never opened and
then exits non zero, so `status=$(curl ... || echo 000)` appended a second
`000` to output curl had already written. The seven status captures in the
workflow all did it, and the sentence somebody reads at seven in the morning
said `GET /signin returned 000000 rather than 200`.

The substitution is now captured and defaulted on its exit status,
`status=$(curl ...) || status=000`, which keeps the one case the fallback
exists for, curl killed before it writes anything, without inventing a six
digit code.

Two of the seven were older than the rest and five were added last week by the
change that fixed this same workflow for claiming a sign-up page had no way in.
The idiom was copied out of the two steps being repaired into the four being
written, which is how a defect in a message outlives the message.

Proved on all seven. Six are reached by pointing the steps at a port with
nothing listening: each printed `000000` before and prints `000` now, and each
still refuses. The seventh, the POST to `/v1/leads`, cannot be reached that way
because the step refuses at the CORS preflight first, so it was reached with a
fixture that answers the preflight and then exits before the POST. The forty
arm falsification the workflow already had passes unchanged, including its six
arms against the live production front door.
