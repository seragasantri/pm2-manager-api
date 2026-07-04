const Docker = require('dockerode');
const path = require('path');
const tar = require('tar-stream');
const { PassThrough } = require('stream');

const docker = new Docker({ socketPath: '/var/run/docker.sock' });

class FileRepository {
  /**
   * Helper: exec command di dalam container dan return output
   */
  static async execInContainer(containerId, cmd) {
    const container = docker.getContainer(containerId);
    const exec = await container.exec({
      Cmd: ['sh', '-c', cmd],
      AttachStdout: true,
      AttachStderr: true,
    });

    const stream = await exec.start({ hijack: true, stdin: false });

    return new Promise((resolve, reject) => {
      let stdout = '';
      let stderr = '';

      stream.on('data', (chunk) => {
        // Docker stream format: 8 byte header
        const type = chunk[0];
        const data = chunk.slice(8).toString('utf8');
        if (type === 1) stdout += data;
        else if (type === 2) stderr += data;
      });

      stream.on('end', () => {
        if (stderr && !stdout) reject(new Error(stderr.trim()));
        else resolve(stdout);
      });

      stream.on('error', reject);
    });
  }

  /**
   * Ambil container ID dan working directory
   */
  static async getAppCwd(appName) {
    const containers = await docker.listContainers({ all: true });
    const container = containers.find(c => {
      const name = (c.Names?.[0] || '').replace(/^\//, '');
      return name === appName;
    });

    if (!container) {
      throw new Error(`Container '${appName}' tidak ditemukan`);
    }

    const cont = docker.getContainer(container.Id);
    const info = await cont.inspect();

    // Cari label 'app.cwd' atau gunakan default /app
    const cwd = info.Config?.Labels?.['app.cwd'] || '/app';

    return {
      cwd,
      containerId: container.Id,
    };
  }

  /**
   * List files di dalam container
   */
  static async listFiles(appName, dir = '') {
    const { cwd, containerId } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, dir);

    if (!targetPath.startsWith(cwd)) {
      throw new Error('Akses direktori ditolak');
    }

    // Gunakan ls untuk list files
    const output = await this.execInContainer(
      containerId,
      `ls -la "${targetPath}" 2>&1`
    );

    // Parse ls output
    const lines = output.trim().split('\n').filter(l => l && !l.startsWith('total'));
    const files = lines.map(line => {
      const parts = line.split(/\s+/);
      const permissions = parts[0];
      const size = parseInt(parts[4]) || 0;
      const name = parts.slice(8).join(' ');

      if (!name || name === '.' || name === '..') return null;

      return {
        name,
        isDirectory: permissions.startsWith('d'),
        path: path.join(dir, name).replace(/\\/g, '/'),
        size,
      };
    }).filter(Boolean);

    return {
      cwd,
      dir,
      files: files.sort((a, b) => {
        if (a.isDirectory && !b.isDirectory) return -1;
        if (!a.isDirectory && b.isDirectory) return 1;
        return a.name.localeCompare(b.name);
      }),
    };
  }

  /**
   * Baca file dari container
   */
  static async readFile(appName, filePath) {
    const { cwd, containerId } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, filePath);

    if (!targetPath.startsWith(cwd)) {
      throw new Error('Path tidak valid');
    }

    const content = await this.execInContainer(containerId, `cat "${targetPath}"`);
    return { content };
  }

  /**
   * Tulis file ke container
   */
  static async writeFile(appName, filePath, content) {
    const { cwd, containerId } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, filePath);

    if (!targetPath.startsWith(cwd)) {
      throw new Error('Path tidak valid');
    }

    // Escape content untuk shell
    const escaped = content.replace(/'/g, "'\\''");
    await this.execInContainer(containerId, `printf '%s' '${escaped}' > "${targetPath}"`);
    return { message: 'File tersimpan' };
  }

  /**
   * Hapus file dari container
   */
  static async deleteFile(appName, filePath) {
    const { cwd, containerId } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, filePath);

    if (!targetPath.startsWith(cwd)) {
      throw new Error('Path tidak valid');
    }

    await this.execInContainer(containerId, `rm -rf "${targetPath}"`);
    return { message: 'Berhasil dihapus' };
  }

  /**
   * Buat direktori di container
   */
  static async createDir(appName, dirPath) {
    const { cwd, containerId } = await this.getAppCwd(appName);
    const targetPath = path.join(cwd, dirPath);

    if (!targetPath.startsWith(cwd)) {
      throw new Error('Path tidak valid');
    }

    await this.execInContainer(containerId, `mkdir -p "${targetPath}"`);
    return { message: 'Folder dibuat' };
  }
}

module.exports = FileRepository;
