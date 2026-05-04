const AppService = require('../service/AppService');
const AppResponse = require('../http/response/AppResponse');

class AppController {
  static async index(req, res) {
    try {
      const apps = await AppService.getApps(req.user);
      return AppResponse.success(res, apps);
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }

  static async action(req, res) {
    try {
      const { name, action } = req.body;

      if (!name || !action) {
        return AppResponse.validationError(res, { name: 'name diperlukan', action: 'action diperlukan' });
      }

      if (!['start', 'stop', 'restart'].includes(action)) {
        return AppResponse.validationError(res, { action: 'Action harus start, stop, atau restart' });
      }

      await AppService.doAction(name, action, req.user);
      return AppResponse.success(res, null, `Aplikasi '${name}' berhasil di-${action}`);
    } catch (error) {
      return AppResponse.error(res, error.message);
    }
  }
}

module.exports = AppController;