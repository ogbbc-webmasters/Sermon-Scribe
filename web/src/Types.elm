module Types exposing (Model, Msg(..), SermonList(..), UploadState(..))

import Api exposing (Sermon)
import File exposing (File)
import Http
import Json.Decode as Decode
import Set exposing (Set)
import Time


type SermonList
    = Loading
    | Loaded (List Sermon)
    | LoadFailed


type UploadState
    = Idle
    | Uploading Float
    | UploadFailed String


type alias Model =
    { sermons : SermonList
    , hasPipelineSnapshot : Bool
    , upload : UploadState
    , confirmingDelete : Maybe Sermon
    , deleting : Set String
    , deletedSermons : Set String
    , deleteError : Maybe String
    , retrying : Set String
    , retryError : Maybe String
    , rerunning : Set String
    , normalizationError : Maybe String
    , zone : Time.Zone
    }


type Msg
    = GotZone Time.Zone
    | GotSermons (Result Http.Error (List Sermon))
    | PipelineEventReceived Decode.Value
    | FilePicked File
    | UploadProgress Http.Progress
    | UploadFinished (Result Http.Error Sermon)
    | RetrySermon Sermon
    | RetryFinished Sermon (Result Http.Error Sermon)
    | RerunNormalization Sermon String
    | RerunNormalizationFinished Sermon (Result Http.Error Sermon)
    | AskDelete Sermon
    | CancelDelete
    | ConfirmDelete Sermon
    | DeleteFinished String (Result Http.Error ())
