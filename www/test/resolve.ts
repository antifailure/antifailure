// Lets a plain `node --test` load this site's modules.
//
// THE PROBLEM, WHICH IS NOT A TYPESCRIPT PROBLEM. Next resolves `./site` to
// `./site.ts` because a bundler does; Node does not, and it will not, because
// extensionless specifiers are not part of the module resolution the runtime
// implements. So every file under lib/ that imports a sibling was unloadable by
// a test runner, and the whole of the beacon was therefore untested.
//
// The alternatives were worse. Writing `./site.ts` in the source needs
// allowImportingTsExtensions in tsconfig.json, and Next 16 rewrites that file
// on every build, which is the exact trap the comment at the top of it
// describes and which four people have already fallen into here. Copying the
// one constant the beacon needs would make two places to change it.
//
// So the fix lives in the test harness: one resolve hook, which tries what Node
// would try and then tries again with .ts on the end. Nothing in the shipped
// site changes, and the rule is a dozen lines somebody can read.

import { registerHooks } from 'node:module'

registerHooks({
  resolve(specifier, context, nextResolve) {
    try {
      return nextResolve(specifier, context)
    } catch (err) {
      // Only a relative specifier with no extension gets a second chance. A
      // bare package name that will not resolve is a missing dependency, and
      // turning that into a confusing file-not-found would hide it.
      const relative = specifier.startsWith('./') || specifier.startsWith('../')
      const bare = !/\.[a-z]+(\?|$)/i.test(specifier)
      if (!relative || !bare) throw err
      return nextResolve(specifier.replace(/(\?|$)/, '.ts$1'), context)
    }
  },
})
