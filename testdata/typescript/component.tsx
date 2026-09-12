export function Component({ready}: {ready: boolean}) {
  return <div>{ready ? "yes" : "no"}</div>;
}
