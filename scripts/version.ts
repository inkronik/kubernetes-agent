const repositoryRoot = new URL('../', import.meta.url)
const semanticVersionSource = String.raw`\d+\.\d+\.\d+(?:-[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*)?`
const semanticVersionPattern = new RegExp(`^${semanticVersionSource}$`)

const replaceDocumentationVersions = (content: string, version: string) =>
    content
        .replace(new RegExp(`--version ${semanticVersionSource}`, 'g'), `--version ${version}`)
        .replace(
            new RegExp(`ghcr\\.io/inkronik/kubernetes-agent:${semanticVersionSource}`, 'g'),
            `ghcr.io/inkronik/kubernetes-agent:${version}`,
        )
        .replace(
            new RegExp(`kubernetes-agent/v${semanticVersionSource}/deploy/kubernetes\\.yaml`, 'g'),
            `kubernetes-agent/v${version}/deploy/kubernetes.yaml`,
        )

const replaceChartDocumentationVersions = (content: string, version: string) => {
    const upgradeSectionMarker = '\n## Upgrade and rollback'
    const markerIndex = content.indexOf(upgradeSectionMarker)
    if (markerIndex === -1) {
        throw new Error('Chart README is missing the upgrade section marker.')
    }

    const releaseDocumentation = content.slice(0, markerIndex)
    const upgradeDocumentation = content.slice(markerIndex)

    return `${replaceDocumentationVersions(releaseDocumentation, version)}${upgradeDocumentation}`
}

const versionedFiles = async (version: string) => {
    const chartPath = 'charts/inkronik-kubernetes-agent/Chart.yaml'
    const manifestPath = 'deploy/kubernetes.yaml'
    const chartDocumentationPath = 'charts/inkronik-kubernetes-agent/README.md'
    const documentationPaths = ['README.md', 'RELEASING.md']
    const chart = await Bun.file(new URL(chartPath, repositoryRoot)).text()
    const manifest = await Bun.file(new URL(manifestPath, repositoryRoot)).text()
    const chartDocumentation = await Bun.file(new URL(chartDocumentationPath, repositoryRoot)).text()
    const documentation = await Promise.all(
        documentationPaths.map(async (path) => ({
            path,
            content: replaceDocumentationVersions(await Bun.file(new URL(path, repositoryRoot)).text(), version),
        })),
    )

    return [
        { path: 'VERSION', content: `${version}\n` },
        {
            path: chartPath,
            content: chart
                .replace(/^version: .+$/m, `version: ${version}`)
                .replace(/^appVersion: .+$/m, `appVersion: "${version}"`),
        },
        {
            path: manifestPath,
            content: manifest.replace(
                new RegExp(`ghcr\\.io/inkronik/kubernetes-agent:${semanticVersionSource}`, 'g'),
                `ghcr.io/inkronik/kubernetes-agent:${version}`,
            ),
        },
        {
            path: chartDocumentationPath,
            content: replaceChartDocumentationVersions(chartDocumentation, version),
        },
        ...documentation,
    ]
}

const packageVersion = async () => {
    const packageManifest = await Bun.file(new URL('package.json', repositoryRoot)).json()

    return String(packageManifest.version)
}

const validateVersion = (version: string) => {
    if (!semanticVersionPattern.test(version)) {
        throw new Error(`Invalid semantic version: ${version}`)
    }
}

const checkVersion = async () => {
    const version = (await Bun.file(new URL('VERSION', repositoryRoot)).text()).trim()
    validateVersion(version)

    const expectedFiles = await versionedFiles(version)
    const mismatchedFiles = (
        await Promise.all(
            expectedFiles.map(async ({ path, content }) => ({
                path,
                matches: (await Bun.file(new URL(path, repositoryRoot)).text()) === content,
            })),
        )
    )
        .filter(({ matches }) => !matches)
        .map(({ path }) => path)

    const currentPackageVersion = await packageVersion()
    const errors = [
        ...(currentPackageVersion === version
            ? []
            : [`package.json has version ${currentPackageVersion}, expected ${version}`]),
        ...(mismatchedFiles.length === 0
            ? []
            : [`Version references are inconsistent in: ${mismatchedFiles.join(', ')}`]),
    ]

    if (errors.length > 0) {
        throw new Error(errors.join('\n'))
    }

    console.log(`Version ${version} is consistent.`)
}

const syncVersion = async (version: string) => {
    validateVersion(version)

    const currentPackageVersion = await packageVersion()
    if (currentPackageVersion !== version) {
        throw new Error(`package.json has version ${currentPackageVersion}, expected ${version}`)
    }

    const files = await versionedFiles(version)
    await Promise.all(files.map(({ path, content }) => Bun.write(new URL(path, repositoryRoot), content)))
    console.log(`Synchronized release version ${version}.`)
}

const mode = Bun.argv.at(2)

if (mode === 'check') {
    await checkVersion()
} else if (mode === 'sync') {
    const version = Bun.argv.at(3)
    if (!version) {
        throw new Error('Release version is required.')
    }
    await syncVersion(version)
} else {
    throw new Error('Usage: bun scripts/version.ts <check|sync> [version]')
}
