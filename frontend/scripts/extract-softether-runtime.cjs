const fs = require('node:fs')
const path = require('node:path')
const zlib = require('node:zlib')

const version = 'v4.42-9798-rtm-2023.06.30'
const fileName = `softether-vpnclient-${version}-windows-x86_x64-intel.exe`
const sourcePath = path.join(__dirname, '..', 'build', fileName)
const outputDir = path.join(__dirname, '..', 'build', 'softether-runtime')

function readPeResources(buffer) {
  const read16 = (offset) => buffer.readUInt16LE(offset)
  const read32 = (offset) => buffer.readUInt32LE(offset)
  const peOffset = read32(0x3c)
  const coffOffset = peOffset + 4
  const optionalOffset = coffOffset + 20
  const optionalMagic = read16(optionalOffset)
  const dataDirectoryOffset = optionalOffset + (optionalMagic === 0x10b ? 96 : 112)
  const resourceRva = read32(dataDirectoryOffset + 16)
  const sectionCount = read16(coffOffset + 2)
  const optionalSize = read16(coffOffset + 16)
  const sections = []
  let sectionOffset = optionalOffset + optionalSize

  for (let index = 0; index < sectionCount; index += 1) {
    sections.push({
      virtualAddress: read32(sectionOffset + 12),
      rawSize: read32(sectionOffset + 16),
      rawOffset: read32(sectionOffset + 20),
    })
    sectionOffset += 40
  }

  const resourceSection = sections.find(
    (section) =>
      resourceRva >= section.virtualAddress &&
      resourceRva < section.virtualAddress + section.rawSize,
  )

  if (!resourceSection) {
    throw new Error('找不到 SoftEther 安装包资源区')
  }

  const resourceBase =
    resourceSection.rawOffset + resourceRva - resourceSection.virtualAddress

  const readName = (value) => {
    if ((value >>> 31) === 0) {
      return `#${value & 0xffff}`
    }

    const nameOffset = resourceBase + (value & 0x7fffffff)
    const length = read16(nameOffset)
    return buffer
      .subarray(nameOffset + 2, nameOffset + 2 + length * 2)
      .toString('utf16le')
  }

  const readEntries = (directoryOffset) => {
    const absoluteOffset = resourceBase + directoryOffset
    const namedCount = read16(absoluteOffset + 12)
    const idCount = read16(absoluteOffset + 14)
    const entries = []

    for (let index = 0; index < namedCount + idCount; index += 1) {
      const entryOffset = absoluteOffset + 16 + index * 8
      entries.push({
        name: readName(read32(entryOffset)),
        offset: read32(entryOffset + 4),
      })
    }

    return entries
  }

  const findResource = (resourceName) => {
    let directoryOffset = 0
    for (const expectedName of ['DATAFILE', resourceName]) {
      const entry = readEntries(directoryOffset).find(
        (item) => item.name.toUpperCase() === expectedName,
      )
      if (!entry || (entry.offset >>> 31) === 0) {
        throw new Error(`SoftEther 安装包缺少资源 ${resourceName}`)
      }
      directoryOffset = entry.offset & 0x7fffffff
    }

    const languageEntry = readEntries(directoryOffset)[0]
    if (!languageEntry || (languageEntry.offset >>> 31) !== 0) {
      throw new Error(`SoftEther 资源 ${resourceName} 没有语言数据`)
    }

    const dataEntryOffset = resourceBase + (languageEntry.offset & 0x7fffffff)
    const dataRva = read32(dataEntryOffset)
    const dataSize = read32(dataEntryOffset + 4)
    const dataSection = sections.find(
      (section) =>
        dataRva >= section.virtualAddress &&
        dataRva < section.virtualAddress + section.rawSize,
    )

    if (!dataSection) {
      throw new Error(`无法定位 SoftEther 资源 ${resourceName}`)
    }

    const dataOffset =
      dataSection.rawOffset + dataRva - dataSection.virtualAddress
    const data = buffer.subarray(dataOffset, dataOffset + dataSize)

    if (resourceName === 'RAW_HAMCORE.SE2') {
      return data
    }

    const expectedSize = data.readUInt32BE(0)
    const uncompressed = zlib.inflateSync(data.subarray(4))
    if (uncompressed.length !== expectedSize) {
      throw new Error(`SoftEther 资源 ${resourceName} 解压后的长度不正确`)
    }
    return uncompressed
  }

  return {
    vpnclient: findResource('VPNCLIENT_X64.EXE'),
    vpncmd: findResource('VPNCMD_X64.EXE'),
    hamcore: findResource('RAW_HAMCORE.SE2'),
  }
}

function readHamcoreFiles(buffer) {
  const magic = buffer.subarray(0, 7).toString('ascii')
  if (magic !== 'HamCore') {
    throw new Error('SoftEther hamcore.se2 格式不正确')
  }

  let offset = 7
  const read32 = () => {
    const value = buffer.readUInt32BE(offset)
    offset += 4
    return value
  }
  const count = read32()
  const files = []

  for (let index = 0; index < count; index += 1) {
    const nameSize = read32()
    const name = buffer.subarray(offset, offset + nameSize - 1).toString('utf8')
    offset += nameSize - 1
    files.push({
      name,
      size: read32(),
      compressedSize: read32(),
      fileOffset: read32(),
    })
  }

  return files.map((file) => {
    const compressed = buffer.subarray(file.fileOffset, file.fileOffset + file.compressedSize)
    const data = zlib.inflateSync(compressed)
    if (data.length !== file.size) {
      throw new Error(`HamCore 文件 ${file.name} 解压后的长度不正确`)
    }
    return { name: file.name, data }
  })
}

function shouldExtractRuntimeFile(name) {
  return name === 'driver_installer.exe'
    || name === 'driver_installer_x64.exe'
    || name === 'empty_sevpnclient.config'
    || name === 'install_src.dat'
    || /^vpninstall_(cn|en|ja)\.inf$/i.test(name)
    || name.startsWith('DriverPackages\\')
}

function writeRuntimeFile(name, data) {
  const outputPath = path.join(outputDir, ...name.split('\\'))
  fs.mkdirSync(path.dirname(outputPath), { recursive: true })
  fs.writeFileSync(outputPath, data)
}

if (!fs.existsSync(sourcePath)) {
  throw new Error(`找不到 SoftEther 安装包：${sourcePath}`)
}

const resources = readPeResources(fs.readFileSync(sourcePath))
fs.rmSync(outputDir, { recursive: true, force: true })
fs.mkdirSync(outputDir, { recursive: true })
fs.writeFileSync(path.join(outputDir, 'vpnclient_x64.exe'), resources.vpnclient)
fs.writeFileSync(path.join(outputDir, 'vpncmd_x64.exe'), resources.vpncmd)
fs.writeFileSync(path.join(outputDir, 'hamcore.se2'), resources.hamcore)
let extractedCount = 0
for (const file of readHamcoreFiles(resources.hamcore)) {
  if (!shouldExtractRuntimeFile(file.name)) continue
  writeRuntimeFile(file.name, file.data)
  extractedCount += 1
}
console.log(`Prepared SoftEther runtime files in ${outputDir} (${extractedCount} driver/support files)`)
