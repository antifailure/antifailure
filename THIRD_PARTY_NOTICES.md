# Third party notices

Antifailure is MIT licensed, except for the `ee/` directory, which is
licensed under the Antifailure Enterprise License. This file lists the
third party software each artifact carries, in a section per artifact: the
af binary, the community control plane image, and what the enterprise
control plane image adds. It is generated from what each one actually
contains rather than maintained by hand, and it travels inside each of
them: in the af release archives, and in both images at
/usr/share/doc/antifailure/THIRD_PARTY_NOTICES.md.

Run `just generate` to regenerate it. `just _generated` and CI both
regenerate it and fail on a difference, so a stale copy cannot be
committed. It used to go stale anyway, because for a long time the
generator ran only while building a release and nothing compared its
output against this file.

## The af binary

The list is the union over every platform a release publishes, because
one release ships all of them and a module can be linked on one platform
and not another. A list taken from a single platform attributes too few
people on every other one.

Platforms: darwin/amd64, darwin/arm64, linux/amd64, linux/arm64.

### Go modules (98)

- `github.com/aymanbagabas/go-osc52/v2` v2.0.1, MIT
- `github.com/cespare/xxhash/v2` v2.3.0, MIT
- `github.com/charmbracelet/bubbletea` v1.3.10, MIT
- `github.com/charmbracelet/colorprofile` v0.4.1, MIT
- `github.com/charmbracelet/lipgloss` v1.1.0, MIT
- `github.com/charmbracelet/x/ansi` v0.11.8, MIT
- `github.com/charmbracelet/x/cellbuf` v0.0.15, MIT
- `github.com/charmbracelet/x/term` v0.2.2, MIT
- `github.com/clipperhouse/displaywidth` v0.11.0, MIT
- `github.com/clipperhouse/uax29/v2` v2.7.0, MIT
- `github.com/containerd/errdefs` v1.0.0, Apache-2.0
- `github.com/containerd/errdefs/pkg` v0.3.0, Apache-2.0
- `github.com/davecgh/go-spew` v1.1.2-0.20180830191138-d8f796af33cc, ISC
- `github.com/distribution/reference` v0.6.0, Apache-2.0
- `github.com/docker/go-connections` v0.8.1, Apache-2.0
- `github.com/docker/go-units` v0.5.0, Apache-2.0
- `github.com/dustin/go-humanize` v1.0.1, MIT
- `github.com/emicklei/go-restful/v3` v3.13.0, MIT
- `github.com/felixge/httpsnoop` v1.1.0, MIT
- `github.com/fxamacker/cbor/v2` v2.9.1, MIT
- `github.com/go-logr/logr` v1.4.4, Apache-2.0
- `github.com/go-logr/stdr` v1.2.2, Apache-2.0
- `github.com/go-openapi/jsonpointer` v1.0.0, Apache-2.0
- `github.com/go-openapi/jsonreference` v1.0.0, Apache-2.0
- `github.com/go-openapi/swag` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/cmdutils` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/conv` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/fileutils` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/jsonutils` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/loading` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/mangling` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/netutils` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/pools` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/stringutils` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/typeutils` v0.27.1, Apache-2.0
- `github.com/go-openapi/swag/yamlutils` v0.27.1, Apache-2.0
- `github.com/google/gnostic-models` v0.7.0, Apache-2.0
- `github.com/google/uuid` v1.6.0, BSD-3-Clause
- `github.com/jackc/pgpassfile` v1.0.0, MIT
- `github.com/jackc/pgservicefile` v0.0.0-20240606120523-5a60cdf6a761, MIT
- `github.com/jackc/pgx/v5` v5.11.0, MIT
- `github.com/jackc/puddle/v2` v2.2.2, MIT
- `github.com/json-iterator/go` v1.1.12, MIT
- `github.com/lucasb-eyer/go-colorful` v1.4.0, MIT
- `github.com/mattn/go-isatty` v0.0.24, MIT
- `github.com/mattn/go-runewidth` v0.0.24, MIT
- `github.com/moby/docker-image-spec` v1.3.1, Apache-2.0
- `github.com/moby/moby/api` v1.56.0, Apache-2.0
- `github.com/moby/moby/client` v0.6.0, Apache-2.0
- `github.com/modern-go/concurrent` v0.0.0-20180306012644-bacd9c7ef1dd, Apache-2.0
- `github.com/modern-go/reflect2` v1.0.3-0.20250322232337-35a7c28c31ee, Apache-2.0
- `github.com/muesli/ansi` v0.0.0-20230316100256-276c6243b2f6, MIT
- `github.com/muesli/cancelreader` v0.2.2, MIT
- `github.com/muesli/termenv` v0.16.0, MIT
- `github.com/munnerz/goautoneg` v0.0.0-20191010083416-a7dc8b61c822, BSD-3-Clause
- `github.com/ncruces/go-strftime` v1.0.0, MIT
- `github.com/opencontainers/go-digest` v1.0.0, Apache-2.0
- `github.com/opencontainers/image-spec` v1.1.1, Apache-2.0
- `github.com/remyoudompheng/bigfft` v0.0.0-20230129092748-24d4a6f8daec, BSD-3-Clause
- `github.com/rivo/uniseg` v0.4.7, MIT
- `github.com/spf13/cobra` v1.10.2, Apache-2.0
- `github.com/spf13/pflag` v1.0.10, BSD-3-Clause
- `github.com/x448/float16` v0.8.4, MIT
- `github.com/xo/terminfo` v0.0.0-20220910002029-abceb7e1c41e, MIT
- `go.opentelemetry.io/auto/sdk` v1.2.1, Apache-2.0
- `go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp` v0.70.0, Apache-2.0 AND BSD-3-Clause
- `go.opentelemetry.io/otel` v1.46.0, Apache-2.0 AND BSD-3-Clause
- `go.opentelemetry.io/otel/metric` v1.46.0, Apache-2.0 AND BSD-3-Clause
- `go.opentelemetry.io/otel/sdk` v1.46.0, Apache-2.0 AND BSD-3-Clause
- `go.opentelemetry.io/otel/trace` v1.46.0, Apache-2.0 AND BSD-3-Clause
- `go.yaml.in/yaml/v2` v2.4.4, Apache-2.0 AND MIT
- `go.yaml.in/yaml/v3` v3.0.5, MIT
- `golang.org/x/crypto` v0.57.0, BSD-3-Clause
- `golang.org/x/net` v0.58.0, BSD-3-Clause
- `golang.org/x/oauth2` v0.36.0, BSD-3-Clause
- `golang.org/x/sync` v0.23.0, BSD-3-Clause
- `golang.org/x/sys` v0.48.0, BSD-3-Clause
- `golang.org/x/term` v0.46.0, BSD-3-Clause
- `golang.org/x/text` v0.42.0, BSD-3-Clause
- `golang.org/x/time` v0.15.0, BSD-3-Clause
- `google.golang.org/protobuf` v1.36.12, BSD-3-Clause
- `gopkg.in/evanphx/json-patch.v4` v4.13.0, BSD-3-Clause
- `gopkg.in/inf.v0` v0.9.1, BSD-3-Clause
- `gopkg.in/yaml.v3` v3.0.1, MIT
- `k8s.io/api` v0.37.0, Apache-2.0
- `k8s.io/apimachinery` v0.37.0, Apache-2.0
- `k8s.io/client-go` v0.37.0, Apache-2.0
- `k8s.io/klog/v2` v2.140.0, Apache-2.0
- `k8s.io/kube-openapi` v0.0.0-20260721132016-d427ff9ee9ad, Apache-2.0
- `k8s.io/utils` v0.0.0-20260626114624-be93311217bd, Apache-2.0
- `modernc.org/libc` v1.75.6, BSD-3-Clause AND MIT
- `modernc.org/mathutil` v1.7.1, BSD-3-Clause
- `modernc.org/memory` v1.12.1, BSD-3-Clause
- `modernc.org/sqlite` v1.58.0, BSD-3-Clause AND LicenseRef-SQLite-public-domain AND MIT
- `sigs.k8s.io/json` v0.0.0-20250730193827-2d320260d730, Apache-2.0 AND BSD-3-Clause
- `sigs.k8s.io/randfill` v1.0.0, Apache-2.0
- `sigs.k8s.io/structured-merge-diff/v6` v6.4.2, Apache-2.0
- `sigs.k8s.io/yaml` v1.6.0, Apache-2.0 AND BSD-3-Clause AND MIT

#### Notices the modules ship

Reproduced as each module ships them, because the Apache License 2.0 asks
in section 4(d) that a NOTICE file distributed with a work be carried with
any redistribution of it.

##### `github.com/go-openapi/jsonpointer` NOTICE

```text
Copyright 2015-2025 go-swagger maintainers

// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

This software library, github.com/go-openapi/jsonpointer, includes software developed
by the go-swagger and go-openapi maintainers ("go-swagger maintainers").

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this software except in compliance with the License.

You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0.

This software is copied from, derived from, and inspired by other original software products.
It ships with copies of other software which license terms are recalled below.

The original software was authored on 25-02-2013 by sigu-399 (https://github.com/sigu-399, sigu.399@gmail.com).

github.com/sigu-399/jsonpointer
===========================

// SPDX-FileCopyrightText: Copyright 2013 sigu-399 ( https://github.com/sigu-399 )
// SPDX-License-Identifier: Apache-2.0

Copyright 2013 sigu-399 ( https://github.com/sigu-399 )

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

##### `github.com/go-openapi/jsonreference` NOTICE

```text
Copyright 2015-2025 go-swagger maintainers

// SPDX-FileCopyrightText: Copyright 2015-2025 go-swagger maintainers
// SPDX-License-Identifier: Apache-2.0

This software library, github.com/go-openapi/jsonreference, includes software developed
by the go-swagger and go-openapi maintainers ("go-swagger maintainers").

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this software except in compliance with the License.

You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0.

This software is copied from, derived from, and inspired by other original software products.
It ships with copies of other software which license terms are recalled below.

The original software was authored on 25-02-2013 by sigu-399 (https://github.com/sigu-399, sigu.399@gmail.com).

github.com/sigh-399/jsonreference
===========================

// SPDX-FileCopyrightText: Copyright 2013 sigu-399 ( https://github.com/sigu-399 )
// SPDX-License-Identifier: Apache-2.0

Copyright 2013 sigu-399 ( https://github.com/sigu-399 )

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

  http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

##### `go.yaml.in/yaml/v2` NOTICE

```text
Copyright 2011-2016 Canonical Ltd.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

##### `go.yaml.in/yaml/v3` NOTICE

```text
Copyright 2011-2016 Canonical Ltd.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

##### `gopkg.in/yaml.v3` NOTICE

```text
Copyright 2011-2016 Canonical Ltd.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
```

##### `modernc.org/libc` LICENSE-3RD-PARTY.md

```text
# Third-Party Software Notices

This repository contains code and assets acquired from third-party sources.
While the main project is licensed under the BSD-3 License, the components
listed below are subject to their own specific license terms and copyright
notices.

The following is a list of third-party software included in this repository,
their locations, and their respective licenses.


----

## Go

* **URL:** https://github.com/golang/go
----

Copyright (c) 2009 The Go Authors. All rights reserved.

Redistribution and use in source and binary forms, with or without
modification, are permitted provided that the following conditions are
met:

   * Redistributions of source code must retain the above copyright
notice, this list of conditions and the following disclaimer.
   * Redistributions in binary form must reproduce the above
copyright notice, this list of conditions and the following disclaimer
in the documentation and/or other materials provided with the
distribution.
   * Neither the name of Google Inc. nor the names of its
contributors may be used to endorse or promote products derived from
this software without specific prior written permission.

THIS SOFTWARE IS PROVIDED BY THE COPYRIGHT HOLDERS AND CONTRIBUTORS
"AS IS" AND ANY EXPRESS OR IMPLIED WARRANTIES, INCLUDING, BUT NOT
LIMITED TO, THE IMPLIED WARRANTIES OF MERCHANTABILITY AND FITNESS FOR
A PARTICULAR PURPOSE ARE DISCLAIMED. IN NO EVENT SHALL THE COPYRIGHT
OWNER OR CONTRIBUTORS BE LIABLE FOR ANY DIRECT, INDIRECT, INCIDENTAL,
SPECIAL, EXEMPLARY, OR CONSEQUENTIAL DAMAGES (INCLUDING, BUT NOT
LIMITED TO, PROCUREMENT OF SUBSTITUTE GOODS OR SERVICES; LOSS OF USE,
DATA, OR PROFITS; OR BUSINESS INTERRUPTION) HOWEVER CAUSED AND ON ANY
THEORY OF LIABILITY, WHETHER IN CONTRACT, STRICT LIABILITY, OR TORT
(INCLUDING NEGLIGENCE OR OTHERWISE) ARISING IN ANY WAY OUT OF THE USE
OF THIS SOFTWARE, EVEN IF ADVISED OF THE POSSIBILITY OF SUCH DAMAGE.

----

## musl libc

* **URL:** https://musl.libc.org/

----

musl as a whole is licensed under the following standard MIT license:

----------------------------------------------------------------------
Copyright © 2005-2020 Rich Felker, et al.

Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
----------------------------------------------------------------------

Authors/contributors include:

A. Wilcox
Ada Worcester
Alex Dowad
Alex Suykov
Alexander Monakov
Andre McCurdy
Andrew Kelley
Anthony G. Basile
Aric Belsito
Arvid Picciani
Bartosz Brachaczek
Benjamin Peterson
Bobby Bingham
Boris Brezillon
Brent Cook
Chris Spiegel
Clément Vasseur
Daniel Micay
Daniel Sabogal
Daurnimator
David Carlier
David Edelsohn
Denys Vlasenko
Dmitry Ivanov
Dmitry V. Levin
Drew DeVault
Emil Renner Berthing
Fangrui Song
Felix Fietkau
Felix Janda
Gianluca Anzolin
Hauke Mehrtens
He X
Hiltjo Posthuma
Isaac Dunham
Jaydeep Patil
Jens Gustedt
Jeremy Huntwork
Jo-Philipp Wich
Joakim Sindholt
John Spencer
Julien Ramseier
Justin Cormack
Kaarle Ritvanen
Khem Raj
Kylie McClain
Leah Neukirchen
Luca Barbato
Luka Perkov
M Farkas-Dyck (Strake)
Mahesh Bodapati
Markus Wichmann
Masanori Ogino
Michael Clark
Michael Forney
Mikhail Kremnyov
Natanael Copa
Nicholas J. Kain
orc
Pascal Cuoq
Patrick Oppenlander
Petr Hosek
Petr Skocik
Pierre Carrier
Reini Urban
Rich Felker
Richard Pennington
Ryan Fairfax
Samuel Holland
Segev Finer
Shiz
sin
Solar Designer
Stefan Kristiansson
Stefan O'Rear
Szabolcs Nagy
Timo Teräs
Trutz Behn
Valentin Ochs
Will Dietz
William Haddon
William Pitcock

Portions of this software are derived from third-party works licensed
under terms compatible with the above MIT license:

The TRE regular expression implementation (src/regex/reg* and
src/regex/tre*) is Copyright © 2001-2008 Ville Laurikari and licensed
under a 2-clause BSD license (license text in the source files). The
included version has been heavily modified by Rich Felker in 2012, in
the interests of size, simplicity, and namespace cleanliness.

Much of the math library code (src/math/* and src/complex/*) is
Copyright © 1993,2004 Sun Microsystems or
Copyright © 2003-2011 David Schultz or
Copyright © 2003-2009 Steven G. Kargl or
Copyright © 2003-2009 Bruce D. Evans or
Copyright © 2008 Stephen L. Moshier or
Copyright © 2017-2018 Arm Limited
and labelled as such in comments in the individual source files. All
have been licensed under extremely permissive terms.

The ARM memcpy code (src/string/arm/memcpy.S) is Copyright © 2008
The Android Open Source Project and is licensed under a two-clause BSD
license. It was taken from Bionic libc, used on Android.

The AArch64 memcpy and memset code (src/string/aarch64/*) are
Copyright © 1999-2019, Arm Limited.

The implementation of DES for crypt (src/crypt/crypt_des.c) is
Copyright © 1994 David Burren. It is licensed under a BSD license.

The implementation of blowfish crypt (src/crypt/crypt_blowfish.c) was
originally written by Solar Designer and placed into the public
domain. The code also comes with a fallback permissive license for use
in jurisdictions that may not recognize the public domain.

The smoothsort implementation (src/stdlib/qsort.c) is Copyright © 2011
Valentin Ochs and is licensed under an MIT-style license.

The x86_64 port was written by Nicholas J. Kain and is licensed under
the standard MIT terms.

The mips and microblaze ports were originally written by Richard
Pennington for use in the ellcc project. The original code was adapted
by Rich Felker for build system and code conventions during upstream
integration. It is licensed under the standard MIT terms.

The mips64 port was contributed by Imagination Technologies and is
licensed under the standard MIT terms.

The powerpc port was also originally written by Richard Pennington,
and later supplemented and integrated by John Spencer. It is licensed
under the standard MIT terms.

All other files which have no copyright comments are original works
produced specifically for use as part of this library, written either
by Rich Felker, the main author of the library, or by one or more
contibutors listed above. Details on authorship of individual files
can be found in the git version control history of the project. The
omission of copyright and license comments in each file is in the
interest of source tree size.

In addition, permission is hereby granted for all public header files
(include/* and arch/*/bits/*) and crt files intended to be linked into
applications (crt/*, ldso/dlstart.c, and arch/*/crt_arch.h) to omit
the copyright notice and permission notice otherwise required by the
license, and to use these files without any requirement of
attribution. These files include substantial contributions from:

Bobby Bingham
John Spencer
Nicholas J. Kain
Rich Felker
Richard Pennington
Stefan Kristiansson
Szabolcs Nagy

all of whom have explicitly granted such permission.

This file previously contained text expressing a belief that most of
the files covered by the above exception were sufficiently trivial not
to be subject to copyright, resulting in confusion over whether it
negated the permissions granted in the license. In the spirit of
permissive licensing, and of not having licensing issues being an
obstacle to adoption, that text has been removed.

----

## go-netdb

* **URL:** https://github.com/dominikh/go-netdb

----

Copyright (c) 2012 Dominik Honnef

Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT.
IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY
CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION OF CONTRACT,
TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.

----

## NixOS/nixpkgs

* **URL:** https://github.com/NixOS/nixpkgs

----

Copyright (c) 2003-2025 Eelco Dolstra and the Nixpkgs/NixOS contributors

Permission is hereby granted, free of charge, to any person obtaining
a copy of this software and associated documentation files (the
"Software"), to deal in the Software without restriction, including
without limitation the rights to use, copy, modify, merge, publish,
distribute, sublicense, and/or sell copies of the Software, and to
permit persons to whom the Software is furnished to do so, subject to
the following conditions:

The above copyright notice and this permission notice shall be
included in all copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND,
EXPRESS OR IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF
MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND
NONINFRINGEMENT. IN NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE
LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY, WHETHER IN AN ACTION
OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION
WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
```

##### `sigs.k8s.io/randfill` NOTICE

```text
When donating the randfill project to the CNCF, we could not reach all the
gofuzz contributors to sign the CNCF CLA. As such, according to the CNCF rules
to donate a repository, we must add a NOTICE referencing section 7 of the CLA
with a list of developers who could not be reached.

`7. Should You wish to submit work that is not Your original creation, You may
submit it to the Foundation separately from any Contribution, identifying the
complete details of its source and of any license or other restriction
(including, but not limited to, related patents, trademarks, and license
agreements) of which you are personally aware, and conspicuously marking the
work as "Submitted on behalf of a third-party: [named here]".`

Submitted on behalf of a third-party: @dnephin (Daniel Nephin)
Submitted on behalf of a third-party: @AlekSi (Alexey Palazhchenko)
Submitted on behalf of a third-party: @bbigras (Bruno Bigras)
Submitted on behalf of a third-party: @samirkut (Samir)
Submitted on behalf of a third-party: @posener (Eyal Posener)
Submitted on behalf of a third-party: @Ashikpaul (Ashik Paul)
Submitted on behalf of a third-party: @kwongtailau (Kwongtai)
Submitted on behalf of a third-party: @ericcornelissen (Eric Cornelissen)
Submitted on behalf of a third-party: @eclipseo (Robert-André Mauchin)
Submitted on behalf of a third-party: @yanzhoupan (Andrew Pan)
Submitted on behalf of a third-party: @STRRL (Zhiqiang ZHOU)
Submitted on behalf of a third-party: @disconnect3d (Disconnect3d)
```

### Container images

An environment starts an emulator when a manifest asks for one, and an
emulator is somebody else's software running beside the application.
It is not linked into the binary, so the module list above cannot see
it, and an image whose licence is recorded by hand goes stale the
first time a digest is bumped. These come from the declarations the
engine starts the containers from.

- LocalStack, Apache License 2.0
  Copyright (c) 2017+ LocalStack contributors, Copyright (c) 2016
  Atlassian Pty Ltd
  - Answers for AWS as `aws`
  - `localstack/localstack` pinned at
  sha256:6b6172cfceb04b4fbc35097a55f717c365a35fafa572be49f7341771cf9023ed
  - https://github.com/localstack/localstack/blob/main/LICENSE.txt
- Azurite, MIT License
  Copyright (c) Microsoft Corporation
  - Answers for Azure as `azure-blob`
  - `mcr.microsoft.com/azure-storage/azurite` pinned at
  sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5
  - https://github.com/Azure/Azurite/blob/main/LICENSE
- Azurite, MIT License
  Copyright (c) Microsoft Corporation
  - Answers for Azure as `azure-queue`
  - `mcr.microsoft.com/azure-storage/azurite` pinned at
  sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5
  - https://github.com/Azure/Azurite/blob/main/LICENSE
- Azurite, MIT License
  Copyright (c) Microsoft Corporation
  - Answers for Azure as `azure-table`
  - `mcr.microsoft.com/azure-storage/azurite` pinned at
  sha256:830430c1da1a2d537e08f3e6764dd1f5ae00cf0346bcaf625b968ec3f0971fd5
  - https://github.com/Azure/Azurite/blob/main/LICENSE
- Google Cloud CLI emulators, Apache License 2.0
  Copyright Google LLC. /google-cloud-sdk/LICENSE inside the image is
  the grant, and it adds that use against a Google Cloud product is
  additionally governed by that product's own terms.
  - Answers for Google Cloud as `bigtable`
  - `gcr.io/google.com/cloudsdktool/google-cloud-cli` pinned at
  sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262
  - https://www.apache.org/licenses/LICENSE-2.0
- Google Cloud CLI emulators, Apache License 2.0
  Copyright Google LLC. /google-cloud-sdk/LICENSE inside the image is
  the grant, and it adds that use against a Google Cloud product is
  additionally governed by that product's own terms.
  - Answers for Google Cloud as `datastore`
  - `gcr.io/google.com/cloudsdktool/google-cloud-cli` pinned at
  sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262
  - https://www.apache.org/licenses/LICENSE-2.0
- Google Cloud CLI emulators, Apache License 2.0
  Copyright Google LLC. /google-cloud-sdk/LICENSE inside the image is
  the grant, and it adds that use against a Google Cloud product is
  additionally governed by that product's own terms.
  - Answers for Google Cloud as `firestore`
  - `gcr.io/google.com/cloudsdktool/google-cloud-cli` pinned at
  sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262
  - https://www.apache.org/licenses/LICENSE-2.0
- fake-gcs-server, BSD 2-Clause License
  Copyright (c) Francisco Souza. Not affiliated with Google.
  - Answers for Google Cloud as `gcs`
  - `fsouza/fake-gcs-server` pinned at
  sha256:797ce226d62f947c009dc40246b30cfb456b8473d8241407f9d6f2c04e4d69ef
  - https://github.com/fsouza/fake-gcs-server/blob/main/LICENSE
- Google Cloud CLI emulators, Apache License 2.0
  Copyright Google LLC. /google-cloud-sdk/LICENSE inside the image is
  the grant, and it adds that use against a Google Cloud product is
  additionally governed by that product's own terms.
  - Answers for Google Cloud as `pubsub`
  - `gcr.io/google.com/cloudsdktool/google-cloud-cli` pinned at
  sha256:07e4b8c3075ca793552fcfaf4808f104ef155d7805d87ade8e01b440463be262
  - https://www.apache.org/licenses/LICENSE-2.0
- Cloud Spanner Emulator, Apache License 2.0
  Copyright Google LLC
  - Answers for Google Cloud as `spanner`
  - `gcr.io/cloud-spanner-emulator/emulator` pinned at
  sha256:4987860c9f8ecf1fffbbcdac115cb88cb9d1a42bd966c235a9ab843aea34fbd1
  - https://github.com/GoogleCloudPlatform/cloud-spanner-emulator/blob/master/LICENSE

### Node packages

The agent runner depends on Playwright, which is Apache 2.0 licensed,
and on its own transitive dependencies. Run `npm ls --all` inside
`runner/` for the full tree of whatever version is installed.

## The community control plane image

What deploy/docker/control-plane.Dockerfile puts into the image, read from
what the image actually carries rather than from a lockfile. The npm
packages come from replaying the image's own npm ci with the same flags.
The console is listed as its static export bundles it, measured from a
build with source maps, with every static asset traced to the file it is
identical to. A licence is read from the text a package ships, and a
package that ships no licence text is attributed from its declaration and
says so.

### npm packages (100)

- `@hono/node-server` 2.1.1, MIT
- `@hono/trpc-server` 0.4.2, MIT (declared, no licence file shipped)
- `@modelcontextprotocol/sdk` 1.30.0, MIT
- `@posthog/core` 1.53.2, Apache-2.0 AND MIT
- `@posthog/types` 1.411.1, Apache-2.0 AND MIT
- `@trpc/server` 11.18.0, MIT
- `accepts` 2.0.0, MIT
- `ajv` 8.20.0, MIT
- `ajv-formats` 3.0.1, MIT
- `body-parser` 2.3.0, MIT
- `bytes` 3.1.2, MIT
- `call-bind-apply-helpers` 1.0.2, MIT
- `call-bound` 1.0.4, MIT
- `content-disposition` 1.1.0, MIT
- `content-type` 1.0.5, MIT
- `content-type` 2.1.0, MIT
- `cookie` 0.7.2, MIT
- `cookie-signature` 1.2.2, MIT
- `cors` 2.8.6, MIT
- `cross-spawn` 7.0.6, MIT
- `debug` 4.4.3, MIT
- `depd` 2.0.0, MIT
- `drizzle-orm` 0.45.2, Apache-2.0 (declared, no licence file shipped)
- `dunder-proto` 1.0.1, MIT
- `ee-first` 1.1.1, MIT
- `encodeurl` 2.0.0, MIT
- `es-define-property` 1.0.1, MIT
- `es-errors` 1.3.0, MIT
- `es-object-atoms` 1.1.2, MIT
- `escape-html` 1.0.3, MIT
- `etag` 1.8.1, MIT
- `eventsource` 3.0.7, MIT
- `eventsource-parser` 3.1.1, MIT
- `express` 5.2.1, MIT
- `express-rate-limit` 8.7.0, MIT
- `fast-deep-equal` 3.1.3, MIT
- `fast-uri` 3.1.7, BSD-3-Clause
- `finalhandler` 2.1.1, MIT
- `forwarded` 0.2.0, MIT
- `fresh` 2.0.0, MIT
- `function-bind` 1.1.2, MIT
- `get-intrinsic` 1.3.0, MIT
- `get-proto` 1.0.1, MIT
- `gopd` 1.2.0, MIT
- `has-symbols` 1.1.0, MIT
- `hasown` 2.0.4, MIT
- `hono` 4.13.7, MIT
- `http-errors` 2.0.1, MIT
- `iconv-lite` 0.7.3, MIT
- `inherits` 2.0.4, ISC
- `ip-address` 10.7.0, MIT
- `ipaddr.js` 1.9.1, MIT
- `is-promise` 4.0.0, MIT
- `isexe` 2.0.0, ISC
- `jose` 6.2.11, MIT
- `json-schema-traverse` 1.0.0, MIT
- `json-schema-typed` 8.0.2, BSD-2-Clause
- `math-intrinsics` 1.1.0, MIT
- `media-typer` 1.1.1, MIT
- `merge-descriptors` 2.0.0, MIT
- `mime-db` 1.54.0, MIT
- `mime-types` 3.0.2, MIT
- `ms` 2.1.3, MIT
- `negotiator` 1.1.0, MIT
- `object-assign` 4.1.1, MIT
- `object-inspect` 1.13.4, MIT
- `on-finished` 2.4.1, MIT
- `once` 1.4.0, ISC
- `parseurl` 1.3.3, MIT
- `path-key` 3.1.1, MIT
- `path-to-regexp` 8.4.2, MIT
- `pkce-challenge` 5.0.1, MIT
- `postgres` 3.4.9, Unlicense (declared, no licence file shipped)
- `posthog-node` 5.52.1, Apache-2.0 AND MIT
- `proxy-addr` 2.0.7, MIT
- `qs` 6.16.0, BSD-3-Clause
- `range-parser` 1.3.0, MIT
- `raw-body` 3.0.2, MIT
- `require-from-string` 2.0.2, MIT
- `router` 2.2.0, MIT
- `safer-buffer` 2.1.2, MIT
- `send` 1.2.1, MIT
- `serve-static` 2.2.1, MIT
- `setprototypeof` 1.2.0, ISC
- `shebang-command` 2.0.0, MIT
- `shebang-regex` 3.0.0, MIT
- `side-channel` 1.1.1, MIT
- `side-channel-list` 1.0.1, MIT
- `side-channel-map` 1.0.1, MIT
- `side-channel-weakmap` 1.0.2, MIT
- `statuses` 2.0.2, MIT
- `toidentifier` 1.0.1, MIT
- `type-is` 2.1.0, MIT
- `typescript` 5.9.3, Apache-2.0
- `unpipe` 1.0.0, MIT
- `vary` 1.1.2, MIT
- `which` 2.0.2, ISC
- `wrappy` 1.0.2, ISC
- `zod` 4.6.2, MIT
- `zod-to-json-schema` 3.25.2, ISC

### The console export (8)

- `@swc/helpers` 0.5.23, Apache-2.0
- `geist` 1.7.2, OFL-1.1
- `next` 16.3.4, MIT
- `next/dist/compiled/process` vendored in next 16.3.4, MIT
- `next/dist/compiled/react` vendored in next 16.3.4, MIT
- `next/dist/compiled/react-dom` vendored in next 16.3.4, MIT
- `next/dist/compiled/react-server-dom-turbopack` vendored in next 16.3.4, MIT
- `next/dist/compiled/scheduler` vendored in next 16.3.4, MIT

## The enterprise control plane image, in addition

The enterprise image carries everything in the community image section
above, and its second install, the eedeps stage of
deploy/docker/control-plane-enterprise.Dockerfile, adds the packages
below. It carries no Go binary of its own: tools/release/build.sh builds
only engine/cmd/af, the release workflow builds nothing else, and the
enterprise image's dockerignore excludes ee/engine, so there are no
enterprise Go modules to attribute.

### npm packages (7)

- `@xmldom/is-dom-node` 1.0.1, MIT
- `@xmldom/xmldom` 0.8.15, MIT
- `@xmldom/xmldom` 0.9.12, MIT
- `xml-crypto` 6.1.2, MIT
- `xpath` 0.0.33, MIT
- `xpath` 0.0.34, MIT
- `yaml` 2.9.0, ISC
