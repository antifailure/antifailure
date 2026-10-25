# fixed

A browser workflow whose time budget ran out while its browser was still
opening could hang the runner for good on Windows. The budget stops the open by
closing the browser, and the open was then meant to fail with whatever call it
had in flight. One call did not fail: when the stop landed while the first page
was being made, as its video recorder started, the browser and the recorder
both exited and Playwright's `newPage` neither resolved nor rejected. Stopping
opens every 2 ms between 108 and 132 ms in on a Windows Server 2022 VM, 4 of 13
never came back within a minute, with no browser left running. The workflow's
verdict never arrived, and the required Windows check timed out a test after
two minutes and then sat until the job's own thirty.

A stop now ends the open itself as soon as the browser has closed, so nothing
waits on a call the closed browser may never answer. Over the same window, 2 ms
apart from 100 to 140 ms and three times over, 9 of 63 opens never came back
before and 0 of 63 after, with no browser left running in either.
