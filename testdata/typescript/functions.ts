export const inspect = (value: number) => {
  // comment-only line
  if (value > 0 && value < 10) {
    const nested = (flag: boolean) => {
      return flag || value === 2 ? value : 0;
    };
    return [value].map((item) => nested(item > 1));
  }
  switch (value) {
    case 20:
      return 20;
    default:
      return value ?? -1;
  }
};

export class Server {
  serve(values: number[]) {
    for (const value of values) {
      try {
        while (value > 1) break;
      } catch (error) {
        return error;
      }
    }
  }
}

declare const socket: {onmessage: () => void};
socket.onmessage = () => {
  if (Math.random()) return;
};
